# XIAO ESP32S3 Sense 单次拍照

这是基于 Henry Yuan 的 [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent) 的 Windows 客户端。镜头按请求拍摄一张 JPEG，完成后固件释放帧缓冲并关闭摄像头驱动。图像通过认证加密连接传回电脑。

希望同时使用 iPhone 控制和镜头时，接上板子的 USB 数据线，运行 `esp32agent pair-usb PORT`，把 `PORT` 换成板子的串口。之后通过局域网运行 `esp32agent photo` 即可。它从这次配对的凭据派生拍照密钥。也可指定一个新文件：

```powershell
esp32agent photo --output ".\capture.jpg"
```

独立工具 `sense-photo` 也支持旧的仅拍照配对。用 `sense-photo --pair-usb PORT` 给当前 Windows 用户保存仅拍照凭据，再运行 `sense-photo`。每块已配对摄像头都记住自己的地址。旧版凭据仅含密钥，首次使用时可用 `--host <板子地址>` 指定地址，或者等板子连上网络后用 USB 再配对一次。默认照片保存在当前用户的“图片/ESP32”中。

板子的静态地址和网络名称通过命令设定。运行 `esp32agent usb PORT wifi status` 可在 USB 上查询板子实际取得的地址。板子开启自己的热点后，用 USB 单独运行 `esp32agent usb PORT wifi hotspot-password` 可查看热点名称和密码。
