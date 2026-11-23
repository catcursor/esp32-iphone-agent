# 用 ESP32 在局域网控制 iPhone

[English](README.md) · 简体中文

把 XIAO ESP32S3 接到 iPhone，就能让电脑上的大模型看屏幕、点按、滑动和输入。iPhone 通过系统「屏幕镜像」把画面投给板子，板子通过 USB 把键鼠输入送回手机。电脑上的大模型直接调用 `esp32agent` 命令。手机无需安装 App，电脑无需配置 MCP 服务。使用 XIAO ESP32S3 Sense 时，还能按需用板载镜头拍实物。

本仓库的开发主线是 `sense-usb-lan`。稳定版 CLI 从本仓库发布。主线每次构建成功后，对应提交的 Actions 页面提供 Windows x64、Linux x64 和 macOS Apple Silicon 的 CLI 压缩包。[稳定版下载](https://github.com/catcursor/esp32-iphone-agent/releases) · [主线 CLI 构建](https://github.com/catcursor/esp32-iphone-agent/actions/workflows/cli.yml?query=branch%3Asense-usb-lan)

## 第一次连接

1. 给 XIAO ESP32S3 刷入本分支固件。在电脑安装 CLI 和 FFmpeg，运行 `esp32agent ocr install`，下载并校验固定版本的 RapidOCR 引擎与模型。Debian 12 可使用系统 Tesseract。[固件与接线](docs/usb-lan.md) · [分平台安装说明](cli/README.zh-CN.md)
2. 用 USB 数据线把板子接到电脑，将下面的 `PORT` 换成实际串口，例如 Windows 的 `COM3` 或 Linux 的 `/dev/ttyACM0`。第一条命令会提示输入 2.4 GHz Wi-Fi 的密码，第二条将配对凭据和板子地址保存到电脑。

   ```sh
   esp32agent usb PORT wifi join "SSID"
   esp32agent pair-usb PORT
   ```

3. 把板子改接到 iPhone。手机连上板子所在的局域网，在「控制中心 → 屏幕镜像」选择 ESP32 Agent，并在「设置 → 辅助功能 → 触控」开启「辅助触控」。电脑运行 `esp32agent device info` 查看连接。
4. 将手机保持竖屏，打开静止页面，运行 `esp32agent calibrate`。完成后，让大模型按仓库的 Skill 调用 CLI。[iphone-lan Skill](skills/iphone-lan/SKILL.md)

## 从截图到一次点按

运行 `esp32agent screenshot --output screen.png`。命令返回图片路径 `data.filePath`、截图尺寸和这一帧的 OCR 结果。每次识别都在电脑本地同步完成。

- **文字目标**　在 `data.ocr.items[]` 选中文字，把该项的 `tap.x,tap.y` 原样传给 `esp32agent io tap X,Y`。同名文字按相邻标题、行或分组区分。
- **图标与开关**　直接查看图片，用控件中心的截图像素坐标。左上角为原点，向右为 X，向下为 Y。
- **确认操作结果**　点按后再截图。若 `data.freshness.status` 为 `unconfirmed`，保持连接并重新截图，等状态为 `current` 再选下一处目标。

## 校准如何影响坐标

`esp32agent calibrate` 以屏幕左边和上边的直边作为参考，分别测量 X、Y 两轴。每个轴把同一指令距离一次走完，再分五次走完。两次实测距离之差除以四，就是每多发一次移动产生的像素偏差。结果见 `data.strokes.x` 和 `data.strokes.y`。程序用测得的比例和偏差计算实际发送的移动量。

校准文件按**板子 ID** 保存在电脑。同一块板换接另一部 iPhone，即使截图宽高相同，也应重新运行 `esp32agent calibrate`。Windows 默认文件为 `%APPDATA%\esp32agent\calibration-<板子ID>.json`。首次位置手势前，程序会核对两个落点，画面尺寸改变后再核对。要单独查看目标位置、实际落点和误差，运行 `esp32agent calibrate --verify`。

## 其他常用操作

| 用途 | 命令 |
| --- | --- |
| 返回主屏幕 | `esp32agent io button HOME` |
| 滑动 | `esp32agent io swipe X1,Y1,X2,Y2` |
| 用 Sense 镜头拍照 | `esp32agent photo --output desk.jpg` |
| 查看板子 IP 策略 | `esp32agent net ipv4` |
| 设置静态 IP 或恢复 DHCP | `esp32agent net static <IPv4>` 或 `esp32agent net dhcp` |
| 更新电脑保存的板子地址 | `esp32agent target <新地址>` |

板子换网后，也可接回电脑，用 `esp32agent usb PORT wifi status` 查实际地址。[网络配置与镜像恢复](docs/usb-lan.md) · [完整命令参考](cli/README.zh-CN.md)

## 项目来源与许可

本项目基于 Henry Yuan 的 [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent)。本分支维护 USB 配对、局域网 CLI、电脑端 OCR、光标校准和 Sense 拍照流程。公开演示、视频与文章请注明原项目来源。[NOTICE](NOTICE) · [MIT 许可证](LICENSE)
