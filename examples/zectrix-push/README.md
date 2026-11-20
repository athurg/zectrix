# Web 推送 Demo

在仓库根目录启动服务。

```sh
go run ./examples/zectrix-push
```

打开 `http://127.0.0.1:32342`，在「设备 IP」栏填入目标地址。浏览器会保存该输入，刷新后自动恢复。也可以在启动时用 `-device` 设置默认设备，支持 IPv4、`IPv4:端口` 和 12 位十六进制设备 ID。

```sh
go run ./examples/zectrix-push -device 192.168.1.100
```

`-listen` 可以覆盖监听地址。需要从局域网其他设备访问时，使用 `-listen 0.0.0.0:32342`。

## HTTP API

向 `POST /api/push` 发送 JSON 或 multipart 表单，通过 `device` 字段指定本次推送的目标。页面和 API 均支持 IPv4 和 `IPv4:端口`。留空时使用启动参数指定的默认设备。没有默认设备时，每次推送都要提供目标地址。

```sh
curl http://127.0.0.1:32342/api/push \
  -H 'Content-Type: application/json' \
  -d '{"device":"192.168.1.100","text":"你好"}'
```

## 构建可执行文件

```sh
go build ./examples/zectrix-push
./zectrix-push -device 192.168.1.100
```
