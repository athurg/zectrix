# ZECTRIX Note 4 局域网协议（LAN v1）—— Go 实现参考

整理自 `zectrix-local-push-sdk`（commit c71412f，SDK 0.1.0 / 固件 3.7.2）的
`docs/zectrix-lan-v1.html`（协议文档 v1.5）以及 `src/*.rs` 的实际实现。
文档没写清、而源码里有的细节，标注为 **[源码]**。

---

## 0. 一页速览

| 项目 | 值 |
|---|---|
| 传输 | UDP，单播，端口 **32342**（以 mDNS SRV 为准） |
| 发现 | mDNS `_zectrix._udp.local.`，TXT：`id` / `model` / `version="1"` |
| 报文 | 16 字节公共头 + 负载，单包 ≤ **1200** 字节，**大端序** |
| 会话 | INFO 拿 `connection_id` → CONNECT → PALETTE → 每 5 秒 PING |
| 失联 | 设备 15 秒收不到新 PING 就退出；PC 15 秒收不到 PING 成功响应也应视为断开 |
| 图片 | 400×300，1/2/4 bpp 打包 → zlib → 1024 字节一块，最多 64 块，用 64 位位图补缺块 |
| 音频 | Opus，16 kHz 单声道，每帧 60 ms（960 采样），一包一帧 |
| 型号 | `zectrix-s3-epaper-4.2`（Note 4；Note 4C 不适用） |
| 固件版本 | **协议里不上报**，只能在设备上查看 |

---

## 1. 发现与连接

### 1.1 发现（mDNS）

- 服务类型：`_zectrix._udp.local.`
- TXT 记录只有三个键：`id`（12 位小写十六进制，即 Wi-Fi MAC）、`model`、`version`（字符串 `"1"`）
- 端口取 SRV 记录（默认 32342），地址只用 IPv4
- 设备平时只应答查询，不主动周期广播。浏览窗口建议 5 秒；查不到不代表设备关机
- 同一个 `id` 可能出现多个地址，要按 `id` 合并
- 发现过程只读，不会取得设备管理权。也可以跳过发现，直接用 `IP:32342`

Go 可选库：`github.com/grandcat/zeroconf` 或 `github.com/hashicorp/mdns`。

### 1.2 连接流程

```
PC                                   设备
 |-- INFO   (conn=0, seq=1, 空负载) -->|  只读，不取得管理权
 |<-- 0x81  (头部 conn = 设备当前编号) --|
 |-- CONNECT(conn=编号, seq=1, 名称) -->|  设备进入 CONNECTING，最多 10 秒
 |<-- 0x82  [0]                       --|  进入 REMOTE，开始计 15 秒保活期限
 |-- PALETTE(conn, 查询 seq, 空)     -->|
 |<-- 0x90  [0, g4×4, g16×16]         --|  21 字节
 |-- 每 5 s PING(conn, 新 ping seq)  -->|
```

1. **INFO**：头部 `connection_id=0`，`sequence=1`，无负载。
   响应头里的 `connection_id` 是设备**当前**的随机 64 位编号，后续所有报文都带它。
2. 校验 INFO 响应（见 §3.1）：管理状态必须不是忙碌、型号要对、尺寸必须是 `400,300,0,0,0,0`。
3. **CONNECT**：用上一步拿到的编号，负载为 `u8 名称长度 + UTF-8 名称`（1～48 字节，不含控制字符）。
   - **[源码]** CONNECT 用控制序号 1；之后控制序号从 2 开始。
   - 重传时编号、序号、内容都不能变。超时上限约 15 秒（设备端准备最多 10 秒）。
   - 设备忙、正在退出、或其他 PC 正在管理时，直接返回 BUSY，不排队。
4. **PALETTE**：CONNECT 成功后立刻查询一次，并严格校验。不通过就发 CLOSE 释放管理权，**不能**退回到写死的默认色板。
5. 启动 **PING 心跳**（§2.3）和**接收循环**（§4）。

### 1.3 断开 / 重连规则（必须遵守）

- 设备端退出（用户同时长按上下键 3 秒、收到 CLOSE、保活超时、断网）后，设备会**立即更换 connection_id**，旧编号的包一律无效。
- 设备会尽力给 PC 发一个 CLOSE 通知（负载里是原因码）；这个通知可能丢失。
- PC 侧**不能**自动重连。只有用户再次显式操作，才能重新走 INFO → CONNECT。
- PC 正常退出：发 `CLOSE`，负载 `[0]`，最多等约 2 秒。
- 任何响应的结果码为 `3 (STALE)`，表示连接已失效，立即进入"已断开"状态。

---

## 2. 报文格式

### 2.1 公共头（16 字节，大端序）

| 偏移 | 长度 | 字段 | 值 |
|---|---|---|---|
| 0 | 2 | magic | `'Z' 'L'`（0x5A 0x4C） |
| 2 | 1 | version | 1 |
| 3 | 1 | type | 消息类型；最高位 `0x80` 表示响应 |
| 4 | 8 | connection_id | INFO 请求填 0 |
| 12 | 4 | sequence | 含义因类型而异 |
| 16 | … | body | 负载 |

整个 UDP 数据报 16～1200 字节。长度不对、或 magic/version 不符的包直接丢弃。

```go
type Packet struct {
    Kind uint8
    Conn uint64
    Seq  uint32
    Body []byte
}

func (p Packet) Encode() []byte {
    b := make([]byte, 16, 16+len(p.Body))
    b[0], b[1], b[2], b[3] = 'Z', 'L', 1, p.Kind
    binary.BigEndian.PutUint64(b[4:], p.Conn)
    binary.BigEndian.PutUint32(b[12:], p.Seq)
    return append(b, p.Body...)
}

func Decode(b []byte) (Packet, bool) {
    if len(b) < 16 || len(b) > 1200 || b[0] != 'Z' || b[1] != 'L' || b[2] != 1 {
        return Packet{}, false
    }
    return Packet{b[3], binary.BigEndian.Uint64(b[4:]), binary.BigEndian.Uint32(b[12:]),
        append([]byte(nil), b[16:]...)}, true
}
```

### 2.2 消息类型

| type | 名称 | 方向 | 请求负载 | 头部 sequence 的含义 |
|---|---|---|---|---|
| 1 | INFO | PC→设备 | 空 | 任意（SDK 用 1） |
| 2 | CONNECT | PC→设备 | `u8 len + name` | 控制序号 |
| 3 | PING | PC→设备 | 空 | PING 序号 |
| 4 | CLOSE | 双向 | `u8 原因` | 控制序号 / 设备事件序号 |
| 5 | IMAGE_BEGIN | PC→设备 | `u8 bpp + u16 压缩长度` | 控制序号，**同时作为图片 ID** |
| 6 | IMAGE_DATA | PC→设备 | `u32 图片ID + ≤1024 字节数据` | 块号（从 0 开始） |
| 7 | IMAGE_STATUS | PC→设备 | `u32 图片ID` | 只读查询序号 |
| 8 | AUDIO_BEGIN | 双向 | `u8 音量 0～100`（设备上行发 `[0]`） | 控制序号，**同时作为流 ID** |
| 9 | AUDIO_DATA | 双向 | `u32 流ID + 一个 Opus 包` | 帧号（从 0 开始） |
| 10 | AUDIO_END | 双向 | `u32 流ID + u32 总帧数 + u16 末帧有效采样数 + u8 原因` | 控制序号 / 事件序号 |
| 11 | AUDIO_STOP | PC→设备 | `u32 流ID` | 控制序号 |
| 12 | STATE | PC→设备 | 空 | 只读查询序号 |
| 13 | RESULT | 设备→PC | `u32 开始序号 + u8 对象类型 + u8 结果` | 设备事件序号 |
| 14 | KEY | 设备→PC | `u8 key + u8 action` | 设备事件序号 |
| 15 | STATUS | PC→设备 | 空 | 只读查询序号（非 0） |
| 16 | PALETTE | PC→设备 | 空 | 只读查询序号（非 0） |

**响应规则：**

- 除两类 DATA 外，每个请求都有响应：`type | 0x80`，回显 `sequence` 和 `connection_id`。
  INFO 是例外：响应头里放的是设备当前的编号。
- 响应负载的第 1 字节总是**结果码**；失败时负载只有这 1 字节。
- **设备发来的事件**（KEY、RESULT、CLOSE、AUDIO_BEGIN、AUDIO_END）PC 必须回 ACK：
  `type|0x80`，回显序号，负载 `[0]`。重复的事件也要回 ACK，但只向上层交付一次（按 `(type, seq)` 去重）。
  AUDIO_DATA 不需要回 ACK。

### 2.3 序号计数器（PC 侧维护三个独立的计数器）

| 计数器 | 用于 | 起始值 |
|---|---|---|
| 控制 | CONNECT、CLOSE、IMAGE_BEGIN、AUDIO_BEGIN、AUDIO_END、AUDIO_STOP | 1（CONNECT 用 1，之后递增） |
| PING | PING | 1 |
| 只读查询 | IMAGE_STATUS、STATE、STATUS、PALETTE | 1（必须非 0） |

- 序号只增不减，不能回绕。快用尽时（接近 `0xFFFFFFFF`）应主动结束连接。
- PC 同一时间只能有**一个**"状态修改类"控制命令在等待响应（SDK 用一把锁保证）。
  CLOSE 和 AUDIO_STOP 可以插队，使用更大的序号。
- 重传必须使用**同一个**序号和同样的内容。设备会对重复请求返回原来的结果，不会重复执行。
  如果收到 `5 OLD_REQUEST`，说明这个序号已经过期，只有该请求失败，连接不受影响。

### 2.4 重传与超时（与 SDK 一致）

- 初始等待 1500 ms，之后每次翻倍，最多 4 s；总期限 15 s。
- 到期仍无响应就是 **ResultUnknown**：请求可能已经执行，**不能**换一个新序号自动重发。
- PING：每 5 秒发一个**新序号**。重复的 PING 不会延长保活期限。
  **[源码]** 单个 PING 等 4.5 s；如果 15 s 内没有任何 PING 成功，就本地断开。
- 收到任意响应且结果码为 3（STALE），立即视为断开。

### 2.5 结果码

| 码 | 名称 | 含义 |
|---|---|---|
| 0 | OK | 成功 |
| 1 | BUSY | 设备忙，不排队；是否重试由业务决定 |
| 2 | INVALID | 参数或长度非法 |
| 3 | STALE | 连接已失效，需要重新连接 |
| 4 | FAILED | 处理失败 |
| 5 | OLD_REQUEST | 序号已过期；只有该请求失败 |

### 2.6 结束原因（CLOSE / AUDIO_END / RESULT / STATE 共用）

`0` 正常 · `1` 设备主动退出 · `2` 保活超时 · `3` 网络变化 · `4` 系统中断 ·
`5` 按键打断 · `6` 采集或时长超限 · `7` 处理失败

---

## 3. 支持的操作（按功能分）

### 3.1 INFO：设备身份（只读，可在连接前调用）

响应负载：

```
u8  result
u8  管理状态         0 LOCAL, 1 CONNECTING, 2 REMOTE, 3 EXITING（非 0 即视为忙）
u8  idLen,  id[idLen]        ASCII，12 位小写 MAC
u8  modelLen, model[...]     UTF-8，"zectrix-s3-epaper-4.2"
u16 width=400, u16 height=300
u16 提示区 x, y, w, h        当前固件全部为 0
```

**[源码]** 尺寸部分必须严格等于这 12 字节：`01 90 01 2C 00 00 00 00 00 00 00 00`。
如果提示区不为 0，说明是保留顶部区域的旧固件，SDK 会拒绝连接。
这一步可以在 CONNECT **之前**完成型号核对。

### 3.2 PALETTE：色板（连接后读取一次）

响应负载正好 21 字节：`[0] + gray4[4] + gray16[16]`。
校验条件：每个表首项为 0、末项为 255、整体非递减（允许重复值）。
数组下标是灰号，值是 0～255 的**线性亮度**（不是 sRGB 值）。黑白模式固定使用 `{0, 255}`。

### 3.3 STATUS：电量 / 充电 / RSSI（只读）

响应负载 4 字节：`[0, battery, charging, rssi]`

- battery：0～100，255 表示未知
- charging：0 否，1 是，255 未知
- rssi：`int8`，范围 -127～0 dBm，127 表示未知

建议查询间隔 ≥ 30 秒，超时 5 秒，同一时间只发一个查询。旧固件会忽略这个请求（表现为超时）。
查询不会延长保活期限。

### 3.4 STATE：当前状态与最近结果（只读）

响应负载 30 字节：

| 偏移 | 类型 | 字段 |
|---|---|---|
| 0 | u8 | result |
| 1 | u8 | 管理状态 |
| 2 | u8 | 图片状态 0 无 / 1 RECEIVING / 2 REFRESHING / 3 DONE / 4 FAILED |
| 3 | u8 | 音频状态 0 空闲 / 1 播放准备 / 2 播放 / 3 采集 / 4 采集收尾 |
| 4 | u32 | 当前图片 ID |
| 8 | u64 | 接收位图 |
| 16 | u32 | 当前音频流 ID |
| 20 | u32 | 最近图片 ID |
| 24 | u8 | 最近图片结束原因 |
| 25 | u32 | 最近播放 ID |
| 29 | u8 | 最近播放结束原因 |

用途：最终的 RESULT 丢失时，用它确认结果。如果"最近 ID"和要查的 ID 对不上，就是结果未知。

### 3.5 显示图片

**像素打包（解压后的原始数据）：**

- 400×300，逐行存储，没有任何文件头
- bpp=1：每行 50 字节，共 15000 字节。**0=白，1=黑**（注意极性是反的）
- bpp=2：每行 100 字节，共 30000 字节。0=黑 … 3=白
- bpp=4：每行 200 字节，共 60000 字节。0=黑 … 15=白，高半字节是左边的像素
- 每个字节中最高位对应最左边的像素
- 解压后的长度必须**恰好**等于 `15000 × bpp`

**[源码] 转换管线**（设备不做二次抖动，抖动必须由 PC 完成）：

1. 把 PNG 当作 sRGB 解码，缩放到 400×300 以内（保持比例，Lanczos3），白底居中
2. 每个通道转线性：`c/12.92`（当 c ≤ 0.04045）否则 `((c+0.055)/1.055)^2.4`
3. 亮度 `Y = 255 × (0.2126R + 0.7152G + 0.0722B)`，alpha 在线性域与白色合成
4. 在色板里选最近的亮度值（距离相同时取最小灰号）；误差用 Floyd–Steinberg 扩散（7/16、3/16、5/16、1/16）
5. 按位深打包；bpp=1 时写入的是 `1 - level`
6. zlib（标准 zlib 头，DEFLATE，等级 6）。压缩后长度必须在 1～65535

```go
// level: 色板下标（Mono 时 0=黑, 1=白）
func put(raw []byte, i int, bpp int, level uint8) {
    v := level
    if bpp == 1 {
        v = 1 - level
    }
    bit := i * bpp
    raw[bit/8] |= v << (8 - bpp - bit%8)
}
// 压缩：zlib.NewWriterLevel(&buf, 6)（compress/zlib 生成的就是标准 zlib 流）
```

**传输流程**（**[源码]** 与 SDK 实现一致）：

```
1. IMAGE_BEGIN  body=[bpp, u16 len]，使用控制序号（走重传）
   响应 OK 后，id = 这条消息的 seq
   BUSY：设备正在接收另一张、后续缓存已满，或正在录音
2. 循环（每 350 ms 一轮）：
   a. 先检查是否已经收到 RESULT(对象=1, id)：结果 0 → 成功，非 0 → 失败
   b. 如果还没进入刷新阶段：每个尚未被确认的块发 IMAGE_DATA
      (seq=块号, body=u32 id + chunk)，块间隔约 2 ms
   c. IMAGE_STATUS(id)，使用查询序号，单次最多等 2 s
      响应 = [0, state, u64 位图]（10 字节）
      位图按 OR 合并（乱序到达的旧响应不能把已收到的块清掉）；bit n 表示第 n 块已收到
      state: 3 DONE → 成功；4 FAILED → 失败；2 REFRESHING → 停止发块，开始等待刷新
   d. 期限：传输阶段总共 30 s；进入 REFRESHING 后最多再等 60 s
      到期后查一次 STATE：最近图片 ID 等于 id 时，按结束原因判定；否则 → ResultUnknown（不能自动重发）
```

关键约定：
- **只有 DONE（或 RESULT=0）才算显示完成**。BEGIN 成功、或所有块都发完，都**不代表**已经显示。
- 设备最多同时处理两张图：一张正在物理刷新，一张已校验的缓存。PC 端应串行上传；更频繁的更新应在 PC 侧合并。
- 设备侧超时：15 秒没有任何新块到达，或接收总计超过 30 秒，图片作废。
- 设备会自动选择刷新波形、清残影；协议里没有"刷新模式"参数。
- 系统通知可能暂时覆盖画面一部分，不影响结果判定。

### 3.6 播放音频（PC → 设备）

- 格式：PCM16 单声道 16 kHz → 用 Opus 编码，每 960 个采样一帧（60 ms）。
  **[源码]** 编码器设置：`Application=Audio`、关闭 DTX，单包最大 1180 字节；最后一帧不足 960 时补 0。
- 时长上限 120 秒，即 1,920,000 个采样，最多 2000 帧。

```
1. AUDIO_BEGIN body=[volume]，使用控制序号；seq 即流 ID
   BUSY：正在录音，或设备提示音正在播放
2. 按时钟节奏发送：第 i 帧在 start + i×60ms 时发出
   AUDIO_DATA seq=i, body=u32 流ID + opus 包（不重传）
   不要一次性把整段灌进去（设备队列上限 6 帧）
   [源码] 如果发送调度落后超过 360 ms，发 AUDIO_STOP 并报错
3. AUDIO_END body = u32 流ID + u32 总帧数 + u16 末帧有效采样数(1～960) + u8 0
   它的响应只代表"收到结束指令"，不代表声音已经播完
4. 等待 RESULT(对象=2, 流ID)；没收到就轮询 STATE（每 500 ms，最多 15 s）
   结果：0 播完；5 被打断（确认键长按开始录音，或待办提醒）；6 超出时长上限；其他视为失败
```

停止播放：`AUDIO_STOP body=u32 流ID`（控制序号，可以插队）。设备空闲时调用也视为成功。
同一时间只能有一个播放，不会自动排队。

### 3.7 录音（设备 → PC，只能由设备按键触发）

PC **无法**远程开始录音。用户长按确认键 ≥ 1 秒后：

```
设备 → AUDIO_BEGIN  seq=流ID, body=[0]           (PC 回 ACK)
设备 → AUDIO_DATA   seq=帧号, body=u32 流ID + opus 包（不需要 ACK）
设备 → AUDIO_END    body=u32 流ID + u32 总帧数 + u16 末帧有效采样数 + u8 原因   (PC 回 ACK)
```

- AUDIO_DATA 可能先于 AUDIO_BEGIN 到达，收到哪种包都要按流 ID 开始一段新录音。
- 新的流 ID 一定更大；小于等于已结束 ID 的包直接丢弃。
- **[源码] 重组逻辑：**
  - 只接受 `next ≤ seq < next+6` 且 `seq < 2000` 的帧
  - 缺帧时用 Opus PLC 补偿；以下情况跳过缺失帧：已缓存 6 帧、或有缓存帧且 180 ms 没有新进展
  - AUDIO_END 到达后再等 360 ms 收尾；最后一帧只保留"末帧有效采样数"个采样
  - 总帧数为 0 时末帧采样数也必须是 0；这种情况没有音频，不要生成文件
  - 3 秒没有任何进展，或单段超过 125 秒，按失败结束
- 录音期间推送图片会返回 BUSY；正在进行的 PC 播放会以原因 5 被打断。
- PC 可以发 `AUDIO_STOP(流ID)` 提前结束当前录音。

### 3.8 按键事件（设备 → PC）

KEY 负载：`key`（1 上 / 2 下 / 3 确认）+ `action`（1 短按 / 2 长按）。

- 实际会产生的事件：确认短按 `[3,1]`，上/下短按 `[1,1] [2,1]`，上/下长按 `[1,2] [2,2]`。
  `[3,2]` 是非法值（确认键长按会进入录音流程）。
- 长短按以 1000 ms 为界，**在松开时**上报。
- 同时长按上下键 3 秒 → 设备本地退出，发 CLOSE(原因 1)，不会产生 KEY 事件。
- 待办通知显示时，确认键用于收起通知，不会上报。
- 设备不会对按键做任何本地处理，也不会自动刷新屏幕，页面逻辑完全由 PC 负责。

### 3.9 CLOSE

- PC → 设备：`body=[0]`，使用控制序号（可以插队），最多等约 2 s。
- 设备 → PC：`body=[原因]`。PC 回 ACK 后进入"已断开"。
- **[源码]** 接收循环因任何原因结束时，SDK 都会用旧编号和新的控制序号补发一个 `CLOSE [0]`，尽力释放管理权。

---

## 4. Go 实现建议

### 4.1 推荐结构

```
zectrix/
  packet.go     编解码、常量、结果码 → error
  discover.go   mDNS 浏览（只读）
  device.go     Open / Close / 三个序号计数器 / 等待响应表 / 重传
  recv.go       单一读循环：分发响应、对事件回 ACK + 去重、录音重组
  image.go      PNG → 线性亮度 → 抖动 → 打包 → zlib；Show() 上传循环
  audio.go      Opus 编解码、Play()/Stop()、Recording 重组
  events.go     Event 类型，容量有界的 channel
```

### 4.2 并发模型

- **一个 `*net.UDPConn`**：用 `net.DialUDP` 连到设备，就能直接 `Write`/`Read`。
- **一个读 goroutine**：
  - 响应包：按 `(type, seq)` 在 `map[key]chan Packet` 里找到等待者并交付
  - 事件包：先回 ACK，再按 `(type, seq)` 去重（保留最近约 256 条），然后投递到事件 channel
  - 只接受 `connection_id` 等于当前编号的包
- **心跳 goroutine**：`time.Ticker(5s)`，每次发一个新序号的 PING。记录最近一次成功时间，超过 15 s 就断开。
- **控制命令**：用 `sync.Mutex` 保证同一时间只有一个在途（CLOSE 和 STOP 除外）。
- **事件 channel 要有容量上限**（SDK 是 16）。消费方太慢时应断开，而不是无限堆积。
- 用 `context.Context` 管理取消；`Close()` 要能安全地重复调用。

### 4.3 请求与重传（参考实现）

```go
func (d *Device) request(kind uint8, seq uint32, body []byte) (Packet, error) {
    key := reqKey{kind | 0x80, seq}
    ch := make(chan Packet, 1)
    d.pendMu.Lock(); d.pending[key] = ch; d.pendMu.Unlock()
    defer func() { d.pendMu.Lock(); delete(d.pending, key); d.pendMu.Unlock() }()

    pkt := Packet{kind, d.conn, seq, body}.Encode()
    deadline := time.Now().Add(15 * time.Second)
    delay := 1500 * time.Millisecond
    for {
        if _, err := d.udp.Write(pkt); err != nil { return Packet{}, err }
        wait := min(delay, time.Until(deadline))
        select {
        case p := <-ch:
            return p, codeErr(p.Body) // 0 → nil；1 Busy；3 Stale(断开)；5 OldRequest …
        case <-d.done:
            return Packet{}, ErrDisconnected
        case <-time.After(wait):
        }
        if !time.Now().Before(deadline) { return Packet{}, ErrResultUnknown }
        delay = min(delay*2, 4*time.Second)
    }
}
```

### 4.4 依赖选择

| 用途 | 建议 |
|---|---|
| mDNS | `github.com/grandcat/zeroconf` |
| PNG 解码 | 标准库 `image/png` |
| 缩放 | `golang.org/x/image/draw`（`CatmullRom` 接近 Lanczos，或自己实现 Lanczos3） |
| zlib | 标准库 `compress/zlib`（等级 6） |
| Opus | `gopkg.in/hraban/opus.v2`（cgo，依赖系统 libopus：`brew install opus pkg-config`）。只做图片和按键可以完全不用它 |
| 文字渲染 | `golang.org/x/image/font/opentype` + `font.Drawer` |

### 4.5 容易踩的坑

1. bpp=1 的极性是 **0=白、1=黑**，和 2/4 bpp 正好相反。
2. 色板值是**线性亮度**。sRGB 128 大约对应线性 55，不能直接拿 sRGB 值和色板比较。
3. PC 端负责抖动，设备不做；也不要在已经抖动过的图上再抖一次。
4. IMAGE_DATA 的头部 seq 是**块号**，不是控制序号；IMAGE_STATUS 用的是**查询序号**。
5. 图片 ID / 流 ID 就是对应 BEGIN 报文的 seq，不用另外分配。
6. 设备发来的 KEY、RESULT、CLOSE、AUDIO_BEGIN、AUDIO_END 都**必须回 ACK**，否则设备会一直重传（最多 15 s）。
7. ResultUnknown 不能自动重发；BUSY 是否重试由业务决定。
8. 断开后不能自动重连；设备那边的编号已经换了。
9. 进程退出前发 CLOSE。进程崩溃时，设备会在 15 s 保活超时后自行释放。
10. 音频要按 60 ms 的节奏发送，不能一次性发完。
11. 协议拿不到固件版本号，只能间接判断：INFO 尺寸字段正确、PALETTE 可用，说明设备支持 SDK 0.1.0 需要的能力。

### 4.6 最小可用顺序（建议的开发步骤）

1. 封包/解包 + INFO（只读，不影响设备），核对 id、型号、尺寸
2. CONNECT + PALETTE + PING + CLOSE，确认设备上出现"远程管理"页面，并且能正常退出
3. 读循环：给 KEY 回 ACK，打印按键
4. 单张 1bpp 图片，等到 DONE；先用纯黑或纯白图验证极性
5. 灰阶 + 抖动
6. STATUS
7. 录音接收（Opus 解码），最后做播放
