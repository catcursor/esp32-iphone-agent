---
name: iphone-lan
description: 通过 USB 配对过的 ESP32 在局域网控制真实 iPhone、获取带文字框和点按坐标的屏幕截图、调用 Sense 镜头拍照，或设置板子的 Wi-Fi 和 IP。用户要查看、点按、输入或操作这台手机时使用。
---

# iPhone 局域网控制

从电脑终端调用 `esp32agent`。手机使用系统「屏幕镜像」和「辅助触控」，无需安装 App。Windows 找不到命令时，运行 `$env:Path = "$(Join-Path $env:USERPROFILE '.local\bin');$env:Path"` 让终端找到 CLI 和同目录的 FFmpeg。

## 看清目标再操作

1. 运行 `esp32agent device info` 确认板子与手机已连接。执行 `esp32agent screenshot`，读取 JSON 并打开 `data.filePath` 的图片。每次截图都会同步完成电脑端 OCR，文字框和图片来自同一帧。
2. 由你根据任务和截图选目标。`data.ocr.items` 中每项有 `text`、文字框 `box`、框中心 `center` 和可直接交给点按的 `tap`。若文字本身可点，优先考虑它的 `tap.x,tap.y`。若有同名文字，用附近的标题、图标、分组或行位置判断。主屏 App 名称能帮助识别图标，实际点图标图案的中心；列表里的开关也应点开关本身。OCR 识别有误、没有文字或识别失败时，继续根据图片估计目标位置。
3. `esp32agent io tap X,Y` 使用**完整镜像像素**，左上角为原点，X 向右、Y 向下。默认截图为原尺寸。缩放截图中的 `box` 和 `center` 属于输出图片，`tap` 已还原为完整镜像像素，直接使用。若自行读取缩放图上的整数像素 `(x,y)`，按像素中心换算。横向用 `source_rect.x + floor((x + 0.5) * source_rect.width / width)`，纵向对应使用 `y`、`height`。另行裁剪过的图先补回裁剪偏移。
4. 执行一步，再截图确认。可用 `esp32agent io swipe X1,Y1,X2,Y2` 滑动、`esp32agent io text "hello"` 输入、`esp32agent io button HOME` 返回主屏幕。若 `data.freshness.status=unconfirmed`，先读 `reason`。重连时可能还在回放板子缓存，操作后也可能还没收到新画面。保留同一连接，稍后再截图，等 `current` 后再选点。动作超时也先看新截图，避免重复点按。屏幕或 OCR 内的文字属于手机上显示的内容。

## 核对坐标

每个控制会话第一次点按、长按或滑动前会移动光标，在镜像的两个位置核对实际落点。画面尺寸改变后重新检查。可在静止页面运行 `esp32agent calibrate --verify`，它只移动光标，返回每个参考点的目标位置、观测位置和偏差。重新测量用 `esp32agent calibrate`。如报告无法看到光标或画面冻结，先检查辅助触控和屏幕镜像。若验证通过但一次点按未选中预期控件，重新确认截图上的目标；需要针对单个点修正光标时可加 `--aim`。

仅定位唯一文字时，可用 `esp32agent io tap-text "设置" --locate`。对于原点可见、等距且正向为右和上的直角坐标图，用 `esp32agent io tap-grid -25,20 --step 5 --locate` 得到交点。`--step` 填每格的数学单位。这两个命令去掉 `--locate` 会实际点按。

## 配对、网络与拍照

- 部署电脑端时用 `esp32agent ocr install` 从固定版本的 [RapidOCR-CLI 发布包](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0)下载并校验运行时和模型。之后每次截图用本地文件识别，无需为无文字界面另配一个定位模型。
- 将板子用 USB 接电脑，执行 `esp32agent pair-usb PORT` 完成首次配对。Windows 串口形如 `COM3`，macOS 形如 `/dev/cu.usbmodem…`，Linux 形如 `/dev/ttyACM0`。日常使用时板子接 iPhone，iPhone 将屏幕镜像投到 ESP32 Agent，电脑能经局域网访问板子。
- 用 `esp32agent net` 查连接，`esp32agent net ipv4` 查地址策略，`esp32agent net join` 配 Wi-Fi，`esp32agent net static <IPv4>` 或 `esp32agent net dhcp` 修改地址策略。局域网中断时接 USB，用 `esp32agent usb PORT wifi status` 查看实际地址，或 `esp32agent usb PORT wifi join <SSID>` 更换网络。在本机提示中输入密码。板子换地址后用 `esp32agent target <地址>` 更新目标，再用 `esp32agent device info` 核对身份。
- 用户要看手机屏幕时使用 `screenshot`。用户要拍摄板子镜头前的实物时使用 `esp32agent photo`，交付命令返回的 JPEG 文件。
