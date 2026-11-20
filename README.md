# Zectrix Go SDK

Zectrix 是用于在局域网内操作 Note 4 设备的 Go SDK，支持设备发现、会话管理、状态查询、按键事件、图像推送和音频播放。

## 安装

需要 Go 1.25 或更新版本。SDK 和实例使用纯 Go，可以在 `CGO_ENABLED=0` 下构建。

在你的 Go 模块中添加依赖。

```sh
go get github.com/athurg/zectrix
```

## 使用示例

下面的程序通过 IPv4 地址查询设备信息。将示例地址换成你的设备 IP。

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/athurg/zectrix"
)

func main() {
	info, err := zectrix.QueryInfo(context.Background(), "192.168.1.100")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(info.ID, info.Model, info.Address)
}
```

文字渲染优先使用与字号匹配的 OpenType 内置点阵（`EBLC`/`EBDT` 中的单字形点阵）。默认字体 `Zfull-GB` 包含 8–24px 的 1bpp 点阵。缺少可用点阵的字号和字符使用 OpenType 轮廓渲染。

## 独立实例

在本仓库根目录运行。每个实例都可用 `-h` 查看参数。

| 实例 | 功能简介 | 运行命令 |
| --- | --- | --- |
| [设备发现](examples/zectrix-discover) | 通过 mDNS 发现局域网中的 Note 4，并列出设备地址和 ID | `go run ./examples/zectrix-discover` |
| [设备信息](examples/zectrix-info) | 只读查询设备 ID、型号、地址和连接状态，不建立会话 | `go run ./examples/zectrix-info -device 192.168.1.100` |
| [设备状态](examples/zectrix-status) | 建立会话，查询电量、充电和 Wi-Fi 状态；可持续监听按键 | `go run ./examples/zectrix-status -device 192.168.1.100` |
| [提示音](examples/zectrix-alert) | 连接设备并依次播放全部内置提示音 | `go run ./examples/zectrix-alert -device 192.168.1.100` |
| [推送](examples/zectrix-push) | 推送文字或图片并播放提示音，也可启动网页和 HTTP API | `go run ./examples/zectrix-push -device 192.168.1.100 -text '你好'` |

[推送实例使用说明](examples/zectrix-push/README.md) · [Note 4 LAN v1 协议参考](docs/note4-lan-v1-protocol.md)
