# 局域网控制与 Sense 拍照

本分支基于 Henry Yuan 的 [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent)。每台电脑通过物理 USB 完成首次配对后，即可通过局域网与板子建立加密连接。

## 准备固件、命令和技能

固件基于 Arduino-ESP32 3.3.12 和 NimBLE-Arduino 2.5.1。使用仓库的 [构建脚本](../hardware/flash.sh) 与其 XIAO ESP32S3 构建参数。Sense 拍照使用摄像头扩展板，传感器实测为 OV5640。摄像头驱动包含 Espressif 的 [PSRAM DMA JPEG 取帧修复](https://github.com/espressif/esp32-camera/pull/871)，许可证随驱动源码提供。

在[本 fork 的发布页](https://github.com/catcursor/esp32-iphone-agent/releases)选择 `cli-v` 版本。Apple Silicon Mac 下载 `esp32agent_darwin_arm64.tar.gz`，Windows x64 下载 `esp32agent_windows_amd64.zip`，Debian 等 x64 Linux 下载 `esp32agent_linux_amd64.tar.gz`。安装后用 `esp32agent version` 核对版本。用 `esp32agent upgrade --check` 手动检查本 fork 的新版本，用 `esp32agent upgrade` 下载并校验当前平台的更新。遇到 GitHub 匿名 API 限流时，客户端会从此仓库最新发布页读取 `versions.json`。旧版客户端首次切换更新源时，先从本 fork 安装新版。手机截图还需要 FFmpeg。

也可在 Windows 上从源码构建。下列 PowerShell 命令从本分支仓库根目录运行，需要 `cli/go.mod` 指定的 Go 版本。

```powershell
$bin = Join-Path $env:USERPROFILE '.local\bin'
New-Item -ItemType Directory -Force $bin | Out-Null
go build -C cli -o (Join-Path $bin 'esp32agent.exe') ./cmd/esp32agent
$env:Path = "$bin;$env:Path"
esp32agent ocr install
$skills = if ($env:CODEX_HOME) { Join-Path $env:CODEX_HOME 'skills' } else { Join-Path $env:USERPROFILE '.codex\skills' }
New-Item -ItemType Directory -Force $skills | Out-Null
Copy-Item -Recurse -Force ./skills/iphone-lan $skills
```

## 配对并使用

1. 用数据线将板子接到电脑，用实际端口替换下例中的 `COM3`。Windows 可在设备管理器查看端口。macOS 通常为 `/dev/cu.usbmodem…`，Debian 等 Linux 通常为 `/dev/ttyACM0`，当前用户需要有该串口的读写权限。
2. 如果板子还没加入你的 Wi-Fi，先运行 `esp32agent usb COM3 wifi join "网络名称"`，在本机提示中输入密码。用 `esp32agent usb COM3 wifi status` 核对地址。ESP32-S3 使用 2.4 GHz Wi-Fi。
3. 运行 `esp32agent pair-usb COM3`。板子保存控制凭据，Windows 用当前用户的 DPAPI 保存本地副本。macOS 和 Linux 将凭据保存在当前用户的私有配置目录中，文件权限为 `0600`，内容以明文保存。拍照的认证密钥从同一份凭据派生。早期独立的拍照凭据继续只授予拍照权限。
4. 把板子用 USB 接到 iPhone。手机与板子加入同一局域网，在控制中心将「屏幕镜像」投到 ESP32 Agent，再开启「辅助触控」。电脑能访问板子的局域网地址即可。
5. 运行 `esp32agent device info` 核对身份和状态，用 `esp32agent screenshot` 验证手机画面。`esp32agent photo` 调用的是板载镜头，拍摄一张 JPEG 并返回本机路径。

| 操作 | 命令示例 |
| --- | --- |
| 保存手机截图并取得同帧文字框 | `esp32agent screenshot --output screen.png` |
| 用 Sense 镜头拍照 | `esp32agent photo --output desk.jpg` |
| 检查已有指针映射、重新校准 | `esp32agent calibrate --verify`、`esp32agent calibrate` |
| 点按、滑动 | `esp32agent io tap 150,300`，`esp32agent io swipe 160,600,160,200` |
| 文本、按键、主屏幕 | `esp32agent io text "hello"`，`esp32agent io keys enter`，`esp32agent io button HOME` |

## 截图和点按共用哪套坐标

部署时在电脑运行 `esp32agent ocr install`，从外部仓库下载并校验固定版本的 [RapidOCR-CLI 运行时与识别模型](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0)。每次执行 `screenshot`，它都会就地识别这一帧，将图片路径和 OCR 结果一起返回。模型可以读取 `data.ocr.items`，按周围文字选中正确的一行；定位图标或开关时也可以直接看图片。

`box` 和 `center` 使用输出图片像素，每项的 `tap.x`、`tap.y` 使用完整镜像像素，可直接传给 `esp32agent io tap X,Y`。默认输出为原尺寸。缩放时保留 `source_width` 和 `source_height`，便于把自行选取的图像点还原。

每个控制会话首次实际点按、长按或滑动之前，CLI 会移动辅助触控光标，将镜像中的两个实测落点与预期坐标比对。如果映射不合格，程序会尝试重新校准，仍无法验证则停止该手势。`esp32agent calibrate --verify` 可主动查看逐点误差，适合在静止、光标可见的页面执行。

## 修改网络

`esp32agent net` 显示连通状态，`esp32agent net ipv4` 显示地址策略。用 `esp32agent net static <IPv4>` 为当前 SSID 保存板端静态地址，或用 `esp32agent net dhcp` 恢复 DHCP。板子从当前网络获取网关、掩码和 DNS，并在使用指定地址前进行 ARP 探测。目标已被占用时会回退到 DHCP。板子保存一份与 SSID 绑定的静态配置，其他网络默认使用 DHCP。

运行 `esp32agent net join` 可在终端输入新 Wi-Fi 的名称和密码。用 `esp32agent net hotspot` 可以切到板子自建的热点。在热点模式下，用 `esp32agent net hotspot-password` 或通过 USB 运行 `esp32agent usb COM3 wifi hotspot-password` 可查看板子生成的热点密码。

变更 Wi-Fi 或 IP 会中断当前连接。用 `esp32agent target <新地址>` 更新电脑保存的目标，再运行 `esp32agent device info`。不知道新地址时，把板子接回电脑，用 `esp32agent usb COM3 wifi status` 查看实际地址。USB 还支持 `wifi static <IPv4>`、`wifi dhcp` 和 `wifi hotspot`。重新执行 `pair-usb` 也会刷新本机目标。

Codex 技能位于 [skills/iphone-lan](../skills/iphone-lan/SKILL.md)，直接调用这些本地命令。更新程序或固件时继续使用此分支的构建产物。
