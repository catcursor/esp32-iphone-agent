# esp32agent

[English](README.md) · 简体中文

本分支的命令行让电脑通过局域网操作真实 iPhone。板子通过系统「屏幕镜像」收画面，通过 USB 向手机发送键鼠输入。大模型在电脑上用 [iphone-lan 技能](../skills/iphone-lan/SKILL.md) 直接调用命令，手机无需安装 App。

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/catcursor/esp32-iphone-agent/sense-usb-lan/cli/install/install.sh | sh
```

Windows（PowerShell）：`irm https://raw.githubusercontent.com/catcursor/esp32-iphone-agent/sense-usb-lan/cli/install/install.ps1 | iex`。
本分支提供 macOS Apple Silicon、Windows x64 和 Linux x64 可执行程序，安装脚本会校验下载包的 SHA-256。用 `esp32agent upgrade --check` 检查更新，用 `esp32agent upgrade` 安装更新。[下载本分支发布版](https://github.com/catcursor/esp32-iphone-agent/releases) · [通过 USB 配对后直连局域网](../docs/usb-lan.md)

## 通过 USB 配对

把板子接到电脑，用实际串口替换 `PORT` 后运行 `esp32agent pair-usb PORT`。
配对后把板子接到 iPhone，再在电脑上运行 `esp32agent device info` 验证局域网连接。
板子的地址改变后，用 `esp32agent target HOST` 更新电脑保存的地址。

## 屏幕、文字框与坐标

开启 iPhone 的「辅助触控」，将「屏幕镜像」投到 ESP32 Agent。运行 `esp32agent screenshot --output screen.png`。它返回文件路径、输出尺寸以及同一帧的本地 OCR。部署时运行 `esp32agent ocr install` 获取固定版本的 [RapidOCR 运行时和模型](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0)。日常识别在本机完成。

`data.ocr.items[]` 的 `text` 是识别文字，`box` 和 `center` 是输出图片上的像素，`tap` 是可直接传给 `io tap X,Y` 的完整镜像坐标。大模型自行选择是否使用文字框，可用周围内容分辨同名文字，或直接看图定位图标。默认按原尺寸截图。缩放时，`width,height` 是输出尺寸，`source_width,source_height` 是完整镜像尺寸。`--output -` 将图片写到标准输出，OCR 元数据写到标准错误。

每个会话首次执行位置手势前，CLI 会在镜像上核对两个光标位置。可运行 `esp32agent calibrate --verify` 读取已有映射的逐点误差，运行 `esp32agent calibrate` 重新测量。校验需要镜像实时更新且光标可见。蓝牙兼容流程见[原项目协议](../docs/cli-protocol.md)。

## 命令

| 命令 | |
|---|---|
| `devices` | 附近的板子（`id`、`paired`、`connected`） |
| `pair`、`unpair` | 配对这台电脑；在这台电脑上忘掉板子 |
| `screenshot [--format png\|jpeg] [--quality N] [--scale F] [--max-size N] [--output PATH\|DIR/\|-]` | 上一个操作结束、画面稳定后的屏幕；默认保存到当前目录 |
| `io tap X,Y [--aim] [--count N]` | 坐标是原尺寸截图的像素；`--count 2` 双击 |
| `io tap-text "文字" [--locate] [--aim]` | 用本地 OCR 找到屏幕上唯一匹配的文字并点其中心。 |
| `io tap-grid X,Y --step N [--locate] [--aim]` | 在浅色等距直角坐标网上按数学坐标点按。两条深色零轴须可见，X 向右、Y 向上。从刻度读出每格的单位数，两轴不同时用 `--step NX,NY`。 |
| `io longpress X,Y [--duration MS]` | |
| `io swipe X1,Y1,X2,Y2` | |
| `io text 'TEXT'` | 仅 ASCII；中文按拼音输入 |
| `io keys COMBO` | `enter`、`esc`、`tab`、`backspace`、`space`、方向键、`cmd+a`、`shift+tab` …… |
| `io button HOME` | 唯一的按键 |
| `screencapture --format h264\|mjpeg` | 实时视频输出到 stdout，例如 `\| ffplay -` |
| `device info` | 板子状态：`ap`（镜像中）、`fz`（画面冻结）、`busy`（App 正在使用手机）、`usb`、`ble` |
| `calibrate [--verify]` | 测量并验证光标映射，或仅检查已有映射 |
| `net` | CLI 当前的通道（`via`：`ble` 或 `wifi`）和板子的 Wi-Fi。电脑和板子在同一个网络（同一个 Wi-Fi，或电脑连着板子热点）时，命令和画面走 Wi-Fi，蓝牙作为备用通道 |
| `net join` | 询问 Wi-Fi 名称和密码（密码不回显），通过蓝牙会话发给板子，并等到通道切到 Wi-Fi。`net join SSID [PASSWORD]` 不询问直接设置。iPhone 也通过这个 Wi-Fi 镜像 |
| `net hotspot` | 让板子重新打开自己的热点 |
| `net via auto\|ble` | `ble` 让会话保持走蓝牙（也可以设 `ESP32AGENT_TRANSPORT=ble`） |
| `listen` | 手机最近 30 秒播放的声音（视频、通话、App 语音）转成的文字；听不到时说明原因 |
| `listen on\|off` | 投屏时收听手机声音（默认关，设置会保存） |
| `listen mode phone\|local` | `phone`（默认）：用 ESP32 Agent App 在手机上转好的文字（在 App 里打开 设置 › 收听手机声音 和 分享给电脑）。`local`：通过 Wi-Fi 拿原始音频，在这台电脑上用 whisper.cpp 转文字 |
| `listen lang CODE\|none` | `local` 模式：英语以外的第二语言，如 `zh`（默认跟随这台电脑的语言） |
| `history list`、`history show RUN\|latest`、`history new` | 在手机上做过什么：运行记录（最新的在前，30 条）、某次运行的步骤；开始新的运行 |
| `intent list`、`intent show NAME\|ID` | 保存的意图 |
| `intent save --name N --prompt P [--hint H ...] [--from-run RUN [--steps 1,3,4]]` | 保存意图；步骤取自某次运行中可以重放的操作（全部，或 `history show` 里的编号） |
| `intent delete NAME\|ID` | |
| `intent export NAME\|ID... [--screenshots] [--output FILE]` | 导出 iOS App 能导入的 `.intent` 文件；加 `--screenshots` 才带参考截图 |
| `intent import FILE` | 导入 `.intent` 文件（来自 App 或 `export`）；名称和提示词都相同的已有意图会跳过 |
| `memory list`、`memory search TEXT [--limit N]` | Agent 记住的内容，最相关的在前 |
| `memory add TEXT [--kind preference\|fact\|app] [--topic APP]` | 记住一行；密码、验证码、银行卡号和证件号会被拒绝 |
| `memory rm ID` | |
| `memory export [--output FILE]`、`memory import FILE` | App 的 `MEMORY.md`；导入时 id 或文字相同的条目保留较新的一份 |
| `report --intent NAME [--screenshots]` | 准备一封邮件，把一个意图发给开发者（见下文）；需要交互 |
| `upgrade [--check] [--force]` | 安装最新版本（`--check`：`current`、`latest`、`update_available`） |
| `mcp` | stdio 上的 MCP 服务器 |
| `skill install` | 把 Agent 技能写到 `~/.claude/skills/esp32agent/` |
| `daemon [stop]`、`version` | |

附近有多块板子时，用 `--device <id>`（12 位十六进制 ID，或板子名称里的 4 位十六进制数）指定，或设置 `ESP32AGENT_DEVICE`。输出是 `{"status":"ok","data":...}` 或 `{"status":"error","error":"..."}`（退出码 1）。需要设备端 Agent 的 mobilecli 命令（`apps`、`dump ui`、`fs`、`webview` 等）会返回 `not supported on ESP32 Agent`。

第一个命令会启动后台的 `esp32agent daemon`，由它保持板子会话并解码视频；其他命令通过配置目录里的 unix socket 连接它（日志：同目录下的 `daemon.log`）。空闲 10 分钟后它会自动退出。

## 接入 Agent

将仓库的 `skills/iphone-lan` 复制到 Codex 技能目录。默认是 `~/.codex/skills/iphone-lan`。Claude Code 可运行 `esp32agent skill install` 安装内置英文技能。两者都直接调用 CLI。使用其他集成时也可运行 `esp32agent mcp`。

## 历史记录和意图

daemon 会像 iOS App 记录运行一样，记下每个操作和截图：配置目录里的 `runs/<yyyyMMdd-HHmmss>/steps.jsonl`（操作用 0–1000 的屏幕单位，画面存成 480 像素宽的 JPEG）。空闲 10 分钟后或运行 `history new` 时开始新的一次运行；保留最新的 100 次。所有内容都不会离开这台电脑。

意图是保存下来的任务：名称、提示词、最多 6 条提示，以及录下的步骤和对应画面（配置目录里的 `intents.json` 和 `intent-frames/<id>/`，字段和 App 一致）。`intent save --from-run RUN` 会取这次运行中可以重放的操作（点按、滑动、输入、按键、回主屏幕）。`intent export` 和 `intent import` 使用 App 的 `.intent` 格式，所以意图可以在 CLI 和 iPhone App 之间互传（隔空投送或「文件」App）。

## 记忆

iOS App 把用户告诉它的内容（偏好、事实、某个 App 怎么用）存在 `MEMORY.md` 里，每条一行，上面带日期。CLI 在配置目录的 `memory/MEMORY.md` 里存同一种文件。互传方法：在 App 里点「设置 › 记忆 › ⋯ › 导出 MEMORY.md」（隔空投送或「文件」App），再运行 `esp32agent memory import FILE`；反过来用 `esp32agent memory export`，再在 App 里点「设置 › 记忆 › ⋯ › 导入」。不经过任何服务器。

## 把意图分享给开发者（可选）

`esp32agent report --intent NAME [--screenshots]` 可以帮助大家改进意图。它只能在你亲自输入的终端里运行（Agent 调用不了，也没有对应的 MCP 工具）。它会在临时目录写一个脱敏的 `.intent`：输入过的文字变成 `•••`，IP 地址会被删掉；加 `--screenshots` 时，每张截图顶部 7%（状态栏、通知）会被涂黑，并缩到 480 像素宽，画面上的其他文字仍然看得清。它会先列出文件里的全部内容（名称、提示词、提示、步骤数、截图、路径），你输入 `y` 才会继续：然后在你的邮件 App 里打开一封发给 `intents@manina.ai` 的邮件，并显示这个文件。附上文件后由你自己发送；esp32agent 不会自己发送任何东西。

## 限制

- iOS App 优先：App 操作手机期间（以及之后 20 秒），指针和键盘命令会失败并返回 `busy: the app is using the phone`。等一会儿再试。
- 走蓝牙时，截图可能比实际画面慢约一秒，动画不连贯；走 Wi-Fi 时几乎实时。
- 点按是相对鼠标移动，从左上角推算位置，每 10 次手势重新归位一次；准确度取决于校准（`calibrate`、`io tap --aim`）。
- 输入仅支持 ASCII（每个字符一次按键）；没有剪贴板，没有音量/电源键，不能安装或直接打开 App。
- 手机声音：只有文字，可能听错。`listen mode local` 需要 Wi-Fi（蓝牙不行）、`brew install whisper-cpp`
  （或 PATH 里有 whisper.cpp 的 `whisper-cli`），以及配置目录 `whisper/` 下的模型 `ggml-base.bin`；
  缺什么 `listen mode local` 会提示。

## 开发

```sh
cd cli && go vet ./... && go test ./...
make -C hardware/tests/sim      # 模拟板子（固件的 cli_link.cpp）：启用集成测试
ESP32AGENT_SIM=$PWD/hardware/tests/sim/cli_sim esp32agent screenshot   # 让 CLI 连模拟器
```

推送 `cli-vX.Y.Z` 稳定版本标签后，GitHub Actions 将三个平台的压缩包、`checksums.txt` 和[版本清单](install/versions.example.json)一起发布到本 fork 的 GitHub Releases。可用 `ESP32AGENT_VERSIONS_URL` 指定自己的清单地址。
