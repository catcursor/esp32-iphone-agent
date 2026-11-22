# ESP32 局域网控制 iPhone

[English](README.md) · 简体中文

用电脑上的大模型操作真实 iPhone。XIAO ESP32S3 接收 iPhone 的系统「屏幕镜像」，通过 USB 向手机发送鼠标和键盘输入。电脑上的 `esp32agent` 通过局域网连接板子，手机无需安装 App。XIAO Sense 还可以按需调用板载摄像头拍一张照片。

## 安装和连接

1. 给板子刷本仓库 `sense-usb-lan` 主线的固件。在电脑安装 CLI 和截图所需的 FFmpeg。支持 Windows x64、Debian 等 Linux x64 和 Apple Silicon Mac。[配对与固件指南](docs/usb-lan.md) · [CLI 安装说明](cli/README.zh-CN.md) · [发布包](https://github.com/catcursor/esp32-iphone-agent/releases)
2. 用数据线将板子接到电脑。运行 `esp32agent usb PORT wifi join "SSID"` 配置 Wi-Fi，再运行 `esp32agent pair-usb PORT` 保存配对凭据和板子地址。`PORT` 替换为实际串口。
3. 把板子接到 iPhone。手机加入同一局域网，在「控制中心 → 屏幕镜像」选择 ESP32 Agent，并在「设置 → 辅助功能 → 触控」打开「辅助触控」。在电脑运行 `esp32agent device info` 查看连接。
4. 为电脑端大模型安装 [iphone-lan 技能](skills/iphone-lan/SKILL.md)。技能直接调用本地命令。

## 从截图到点按

执行 `esp32agent screenshot --output screen.png`，可同时取得画面和这一帧的 OCR 结果。部署阶段从 [RapidOCR-CLI 的固定版本](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0)取得引擎和模型，之后每次截图都在电脑本地同步识别。

大模型自己决定怎么选目标。有文字时可以取文字框中心，出现同名文字时结合周围的图标、行或分组判断。没有文字时直接看截图估计控件中心。用 `esp32agent io tap X,Y` 点按，坐标以完整截图左上角为原点。每次操作后重新截图，核对页面变化。

每个控制会话第一次点按、长按或滑动之前，程序将光标移到两个参考点，对照镜像中实际看到的位置。两个点都须满足横向误差不超过镜像宽度的 1.5%、纵向误差不超过镜像高度的 1.5%。画面尺寸改变后重新检查。可用 `esp32agent calibrate --verify` 主动检查已有映射，或用 `esp32agent calibrate` 重新测量。检查覆盖截图像素到手机光标的映射，模型是否选中了正确控件还需看操作后的画面。

用 `esp32agent photo --output desk.jpg` 可以从 Sense 镜头拍实物。静态 IP、DHCP 和 USB 网络恢复见[局域网指南](docs/usb-lan.md)。

## 项目来源

本项目基于 Henry Yuan 的 [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent)。原项目提供 iOS App、蓝牙流程与硬件设计说明。分发及公开演示的署名说明见 [NOTICE](NOTICE) 与 [MIT 许可证](LICENSE)。
