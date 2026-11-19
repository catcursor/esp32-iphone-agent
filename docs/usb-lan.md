# Windows 局域网控制与 Sense 拍照

本分支基于 Henry Yuan 的 [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent)。使用本分支的固件和 Windows 命令行程序，可以通过物理 USB 完成首次配对。之后在电脑上运行命令即可通过局域网与板子建立加密连接。

## 准备固件、命令和技能

固件基于 Arduino-ESP32 3.3.12 和 NimBLE-Arduino 2.5.1。使用仓库的 [构建脚本](../hardware/flash.sh) 与其 XIAO ESP32S3 构建参数。Sense 拍照使用摄像头扩展板，传感器实测为 OV5640。摄像头驱动包含 Espressif 的 [PSRAM DMA JPEG 取帧修复](https://github.com/espressif/esp32-camera/pull/871)，许可证随驱动源码提供。

在 Windows 上从本分支仓库根目录运行下列 PowerShell 命令。需要 `cli/go.mod` 指定版本的 Go，以及用于解码手机截图的 FFmpeg。

```powershell
$bin = Join-Path $env:USERPROFILE '.local\bin'
New-Item -ItemType Directory -Force $bin | Out-Null
go build -C cli -o (Join-Path $bin 'esp32agent.exe') ./cmd/esp32agent
$env:Path = "$bin;$env:Path"
$skills = if ($env:CODEX_HOME) { Join-Path $env:CODEX_HOME 'skills' } else { Join-Path $env:USERPROFILE '.codex\skills' }
New-Item -ItemType Directory -Force $skills | Out-Null
Copy-Item -Recurse -Force ./skills/iphone-lan $skills
```

## 配对并使用

1. 用数据线将板子接到 Windows。用设备管理器看清串口号，用实际端口替换下例中的 `COM3`。
2. 如果板子还没加入你的 Wi-Fi，先运行 `esp32agent usb COM3 wifi join "网络名称"`，在本机提示中输入密码。用 `esp32agent usb COM3 wifi status` 核对地址。ESP32-S3 使用 2.4 GHz Wi-Fi。
3. 运行 `esp32agent pair-usb COM3`。板子保存控制凭据，Windows 用当前用户的 DPAPI 保存本地副本。拍照的认证密钥从同一份凭据派生。早期独立的拍照凭据继续只授予拍照权限。
4. 把板子用 USB 接到 iPhone。手机与板子加入同一局域网，在控制中心将「屏幕镜像」投到 ESP32 Agent，再开启「辅助触控」。电脑能访问板子的局域网地址即可。
5. 运行 `esp32agent device info` 核对身份和状态，用 `esp32agent screenshot` 验证手机画面。`esp32agent photo` 调用的是板载镜头，拍摄一张 JPEG 并返回本机路径。

| 操作 | 命令示例 |
| --- | --- |
| 保存手机截图 | `esp32agent screenshot --output screen.png` |
| 用 Sense 镜头拍照 | `esp32agent photo --output desk.jpg` |
| 校准指针 | `esp32agent calibrate` |
| 点按、滑动 | `esp32agent io tap 150,300`，`esp32agent io swipe 160,600,160,200` |
| 文本、按键、主屏幕 | `esp32agent io text "hello"`，`esp32agent io keys enter`，`esp32agent io button HOME` |

## 修改网络

`esp32agent net` 显示连通状态，`esp32agent net ipv4` 显示地址策略。用 `esp32agent net static <IPv4>` 为当前 SSID 保存板端静态地址，或用 `esp32agent net dhcp` 恢复 DHCP。板子从当前网络获取网关、掩码和 DNS，并在使用指定地址前进行 ARP 探测。目标已被占用时会回退到 DHCP。板子保存一份与 SSID 绑定的静态配置，其他网络默认使用 DHCP。

运行 `esp32agent net join` 可在终端输入新 Wi-Fi 的名称和密码。用 `esp32agent net hotspot` 可以切到板子自建的热点。在热点模式下，用 `esp32agent net hotspot-password` 或通过 USB 运行 `esp32agent usb COM3 wifi hotspot-password` 可查看板子生成的热点密码。

变更 Wi-Fi 或 IP 会中断当前连接。用 `esp32agent target <新地址>` 更新电脑保存的目标，再运行 `esp32agent device info`。不知道新地址时，把板子接回电脑，用 `esp32agent usb COM3 wifi status` 查看实际地址。USB 还支持 `wifi static <IPv4>`、`wifi dhcp` 和 `wifi hotspot`。重新执行 `pair-usb` 也会刷新本机目标。

Codex 技能位于 [skills/iphone-lan](../skills/iphone-lan/SKILL.md)，直接调用这些本地命令。更新程序或固件时继续使用此分支的构建产物。
