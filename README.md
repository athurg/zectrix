# Zectrix

Zectrix 提供 Note 4 设备的命令行工具和 Web 演示页面。

## 直接运行

需要 Go 1.25 或更新版本、支持 CGO 的 C 编译器、`pkg-config` 和 FreeType 开发库。构建前可用 `pkg-config --modversion freetype2` 检查 FreeType 是否可用。

在项目根目录运行以下命令，查找局域网中的设备。

```sh
go run ./cmd/zectrix discover
```

启动 Web 演示时，可以用 `-device` 设置默认设备，支持 IPv4 地址、`IPv4:端口` 或 12 位十六进制设备 ID。也可以直接运行 `go run ./cmd/zectrix demo`，在页面或 API 请求中指定设备。

```sh
go run ./cmd/zectrix demo -device 192.168.1.100
```

在浏览器中打开 `http://127.0.0.1:32342`。Web 服务的 TCP 端口默认与设备的 UDP 端口同为 `32342`。需要从局域网内的其他设备访问时，加上 `-listen 0.0.0.0:32342`。

在页面的「设备 IP」栏填写目标地址，或在 `POST /api/push` 的 JSON 或 multipart 表单中传入 `device` 字段。两种方式都支持 IPv4 和 `IPv4:端口`，并覆盖本次推送的目标。留空时使用启动参数指定的默认设备。没有默认设备时，每次推送都要提供地址。

```sh
curl http://127.0.0.1:32342/api/push \
  -H 'Content-Type: application/json' \
  -d '{"device":"192.168.1.100","text":"你好"}'
```

也可以先构建可执行文件，再直接运行。

```sh
go build -o zectrix ./cmd/zectrix
./zectrix demo -device 192.168.1.100
```
