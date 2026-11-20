# 推送 Demo

## 命令行推送

在仓库根目录运行。默认直接连接设备，推送完成后退出。

```sh
go run ./examples/zectrix-push -device 192.168.1.100
go run ./examples/zectrix-push -device 192.168.1.100 -title '通知' -text '你好'
go run ./examples/zectrix-push -device 192.168.1.100 -image ./picture.png -sound Pop -volume 70
```

`-device` 接受 IPv4、`IPv4:端口` 和 12 位十六进制设备 ID。默认推送标题为 `通知` 的 11 行测试正文，标题 16px、正文 14px，音效 Glass、音量 80，保持 10 秒。用 `-text` 或 `-image` 替换默认正文。`-hold` 以秒为单位，设为 `0` 会等待设备按键。用 `-h` 查看全部选项。

## Web 模式

加 `-web` 启动服务。推送参数会作为页面和 API 的默认值。

```sh
go run ./examples/zectrix-push -web -device 192.168.1.100 -title '通知' -text '你好' -volume 70
```

打开 `http://127.0.0.1:32342`。页面上填写的值和浏览器已保存的值优先于启动默认值。使用 `-image` 启动时，页面会默认选中「使用启动时指定的图片」，也可以上传其他图片或取消勾选后输入正文。

`-listen` 可以覆盖 Web 监听地址。需要从局域网其他设备访问时，使用 `-listen 0.0.0.0:32342`。

## HTTP API

向 `POST /api/push` 发送 JSON 或 multipart 表单。请求可用 `device` 指定 IPv4 或 `IPv4:端口`，留空时使用启动默认设备。省略 `title`、`text` 与 `image`、`sound`、`volume`、`hold`、`title_size` 或 `body_size` 时，继承对应的启动默认值。显式提供正文或图片会替换启动时的内容。

```sh
curl http://127.0.0.1:32342/api/push \
  -H 'Content-Type: application/json' \
  -d '{"device":"192.168.1.100","text":"你好"}'
```

## 构建可执行文件

```sh
go build ./examples/zectrix-push
./zectrix-push -device 192.168.1.100 -text '你好'
./zectrix-push -web
```
