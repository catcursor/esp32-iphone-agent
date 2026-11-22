# esp32agent

English · [简体中文](README.zh-CN.md)

This fork’s CLI controls a real iPhone from a computer over the LAN. The board receives the iPhone’s Screen Mirroring stream and sends USB mouse and keyboard input. The computer’s agent calls the CLI directly using the [iphone-lan skill](../skills/iphone-lan/SKILL.md). The phone needs no companion app.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/catcursor/esp32-iphone-agent/sense-usb-lan/cli/install/install.sh | sh
```

Windows (PowerShell): `irm https://raw.githubusercontent.com/catcursor/esp32-iphone-agent/sense-usb-lan/cli/install/install.ps1 | iex`.
Download a build for macOS Apple Silicon, Windows x64 or Linux x64 from [this fork's releases](https://github.com/catcursor/esp32-iphone-agent/releases). The installers check the SHA-256 of each archive. Use `esp32agent upgrade --check` to check for a newer CLI and `esp32agent upgrade` to install it. For direct LAN control, use the [USB pairing guide](../docs/usb-lan.md).

## Pair over USB

Connect the board to your computer and run `esp32agent pair-usb PORT` with its serial port.
After pairing, move the board to the iPhone and run `esp32agent device info` over the LAN.
`esp32agent target HOST` updates the board address saved on the computer.

## Screen, text boxes and coordinates

Enable AssistiveTouch and mirror the iPhone screen to ESP32 Agent. Run `esp32agent screenshot --output screen.png`. It returns the file path, dimensions and local OCR from that frame. Run `esp32agent ocr install` at deployment to fetch the pinned [RapidOCR runtime and models](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0). Recognition runs on the computer.

The Linux x64 RapidOCR build requires glibc 2.38 or later, available on Debian 13 and newer. On Debian 12 (glibc 2.36), screenshot OCR can use the host's Tesseract. Install English and Simplified Chinese recognition with `sudo apt install tesseract-ocr tesseract-ocr-eng tesseract-ocr-chi-sim`.

Each item in `data.ocr.items[]` has its recognized `text`, a `box` and `center` in output-image pixels and a `tap` point ready for `io tap X,Y` in full-screen pixels. The agent can distinguish repeated labels by context or use the image directly for icons. The default image is full size. On resized output, `width,height` describe the saved image and `source_width,source_height` the original frame. With `--output -`, stdout contains the image and stderr carries the OCR metadata. A freshness status of `unconfirmed` means the client is replaying buffered video or waiting for a frame after the latest input. Keep the session open and capture again before choosing another point.

Before the first positional gesture of a session the CLI checks the pointer against two locations on the mirrored screen. Run `esp32agent calibrate --verify` for per-point error on the saved mapping. Run `esp32agent calibrate` to remeasure. Both require a live mirrored screen and a visible pointer. The [upstream protocol](../docs/cli-protocol.md) documents Bluetooth compatibility.

## Commands

| Command | |
|---|---|
| `devices` | boards nearby (`id`, `paired`, `connected`) |
| `pair`, `unpair` | pair this computer; forget the board here |
| `screenshot [--format png\|jpeg] [--quality N] [--scale F] [--max-size N] [--output PATH\|DIR/\|-]` | the screen after the last action settled; default a file in the current directory |
| `io tap X,Y [--aim] [--count N]` | coordinates are pixels of a full-size screenshot; `--count 2` double-taps |
| `io tap-text "LABEL" [--locate] [--aim]` | find a unique matching label on the live screen with local OCR and tap its center.  |
| `io tap-grid X,Y --step N [--locate] [--aim]` | tap a point in graph units on an axis-aligned, evenly spaced light grid with visible darker zero axes (right/up). Read the step from the tick labels. Use `--step NX,NY` for different horizontal and vertical units per cell. |
| `io longpress X,Y [--duration MS]` | |
| `io swipe X1,Y1,X2,Y2` | |
| `io text 'TEXT'` | ASCII; Chinese is typed as pinyin |
| `io keys COMBO` | `enter`, `esc`, `tab`, `backspace`, `space`, arrows, `cmd+a`, `shift+tab` ... |
| `io button HOME` | the only button |
| `screencapture --format h264\|mjpeg` | live video to stdout, e.g. `\| ffplay -` |
| `device info` | board status: `ap` (mirroring), `fz` (video frozen), `busy` (the app holds the phone), `usb`, `ble` |
| `calibrate [--verify]` | measure and verify the pointer, or check its saved mapping |
| `net` | the CLI's link (`via`: `ble` or `wifi`) and the board's Wi-Fi. With this computer on the board's network (same Wi-Fi, or its hotspot) commands and video go over Wi-Fi; BLE stays as the way back |
| `net join` | asks for your Wi-Fi's name and password (not echoed), sends them to the board over the Bluetooth session, and waits until the link moves to Wi-Fi. `net join SSID [PASSWORD]` does it without asking. The iPhone mirrors over that Wi-Fi too |
| `net hotspot` | the board opens its own hotspot again |
| `net via auto\|ble` | `ble` keeps the session on BLE (also `ESP32AGENT_TRANSPORT=ble`) |
| `listen` | what the iPhone played in the last 30 s (a video, a call, an app's voice) as words, or why it can't be heard |
| `listen on\|off` | hear the phone's sound while it mirrors (off by default; kept in the config) |
| `listen mode phone\|local` | `phone` (default): the words the ESP32 Agent app transcribed on the iPhone (turn on Settings › Listen to phone audio and Share with the computer there). `local`: the raw audio over Wi-Fi, transcribed here by whisper.cpp |
| `listen lang CODE\|none` | `local`: the language besides English, e.g. `zh` (default: this computer's language) |
| `history list`, `history show RUN\|latest`, `history new` | what was done on the phone: runs (newest first, 30), one run's steps; start a new run |
| `intent list`, `intent show NAME\|ID` | saved intents |
| `intent save --name N --prompt P [--hint H ...] [--from-run RUN [--steps 1,3,4]]` | save an intent; steps are the run's replayable moves (all, or the numbers from `history show`) |
| `intent delete NAME\|ID` | |
| `intent export NAME\|ID... [--screenshots] [--output FILE]` | `.intent` file the iOS app imports; reference screens only with `--screenshots` |
| `intent import FILE` | an `.intent` file (from the app or `export`); ones already saved under the same name and prompt are skipped |
| `memory list`, `memory search TEXT [--limit N]` | what the agent remembers, the most related first |
| `memory add TEXT [--kind preference\|fact\|app] [--topic APP]` | remember one line; passwords, codes and card or ID numbers are refused |
| `memory rm ID` | |
| `memory export [--output FILE]`, `memory import FILE` | the app's `MEMORY.md`; on import an entry with the same id or words keeps the newer one |
| `report --intent NAME [--screenshots]` | prepare a mail with one intent for the developer (see below); interactive |
| `upgrade [--check] [--force]` | install the latest release (`--check`: `current`, `latest`, `update_available`) |
| `mcp` | MCP server on stdio |
| `skill install` | writes the agent skill to `~/.claude/skills/esp32agent/` |
| `daemon [stop]`, `version` | |

With several boards around pass `--device <id>` (the 12-hex id or the 4 hex digits of its name) or set
`ESP32AGENT_DEVICE`. Output is `{"status":"ok","data":...}` or `{"status":"error","error":"..."}` (exit code 1).
mobilecli commands that need an on-device agent (`apps`, `dump ui`, `fs`, `webview`, ...) answer
`not supported on ESP32 Agent`.

The first command starts a background `esp32agent daemon` that holds the board connection and decodes video;
commands reach it over a unix socket in the config dir (log: `daemon.log` there). It exits after 10 minutes idle.

## Agents

Copy `skills/iphone-lan` into the Codex skills directory, normally `~/.codex/skills/iphone-lan`. For Claude Code, run `esp32agent skill install` to write the embedded English skill. Both skills use the CLI directly. The `esp32agent mcp` entry point is also available for other integrations.

## History and intents

The daemon records every action and screenshot as the iOS app records its runs: `runs/<yyyyMMdd-HHmmss>/steps.jsonl`
in the config dir (actions in 0-1000 screen units, screens as 480-wide JPEGs). A new run starts after 10 minutes
idle or with `history new`; the newest 100 are kept. Nothing leaves the computer.

An intent is a saved task: name, prompt, up to 6 hints and recorded steps with their screens (`intents.json` and
`intent-frames/<id>/` in the config dir, the app's fields). `intent save --from-run RUN` takes the run's replayable
moves (tap, swipe, type, key, home). `intent export` and `intent import` use the app's `.intent` format, so
intents move between the CLI and the iPhone app (AirDrop or Files).

## Memory

The iOS app keeps what the user told it (preferences, facts, how an app works) in `MEMORY.md`, one entry per
line under a dated header. The CLI keeps the same file in `memory/MEMORY.md` in the config dir. To move it: in the
app, Settings › Memory › ⋯ › Export MEMORY.md (AirDrop or Files), then `esp32agent memory import FILE`; the other
way, `esp32agent memory export` and Settings › Memory › ⋯ › Import. Nothing is synced through a server.

## Share an intent with the developer (optional)

`esp32agent report --intent NAME [--screenshots]` helps improve intents for everyone. It only runs in a terminal
you type in (agents can't call it, and there is no MCP tool for it). It writes a redacted `.intent` to a temp
folder: typed text becomes `•••`, IP addresses are removed, and with `--screenshots` each screen gets the top 7%
(status bar, notifications) blacked out and is shrunk to 480 px wide; other text on the screens stays readable.
It prints exactly what the file holds (name, prompt, hints, step count, screenshots, path) and continues only when
you type `y`: then it opens a mail to `intents@manina.ai` in your mail app and shows the file. Attach it and send
it yourself; esp32agent never sends anything.

## Limits

- The iOS app has priority: while it drives the phone (and 20 s after), pointer and keyboard commands fail with
  `busy: the app is using the phone`. Wait and retry.
- Over Bluetooth, screenshots can lag up to about a second and animations are choppy; over Wi-Fi they are close to live.
- Taps are relative mouse moves, dead-reckoned from the top-left corner and re-homed every 10 gestures; accuracy
  depends on calibration (`calibrate`, `io tap --aim`).
- Typing is ASCII only (one key per character); no clipboard, no volume/power buttons, no app install or launch.
- Phone audio: words only, and they can be misheard. `listen mode local` needs Wi-Fi (not Bluetooth),
  `brew install whisper-cpp` (or a build of whisper.cpp's `whisper-cli` on PATH) and the model
  `ggml-base.bin` in the config dir's `whisper/` folder; `listen mode local` says what is missing.

## Development

```sh
cd cli && go vet ./... && go test ./...
make -C hardware/tests/sim      # simulated board (the firmware's cli_link.cpp): enables the integration test
ESP32AGENT_SIM=$PWD/hardware/tests/sim/cli_sim esp32agent screenshot   # the CLI against the simulator
```

Push a stable tag such as `cli-vX.Y.Z` to release all three builds, `checksums.txt` and the versioned [manifest](install/versions.example.json) on this fork's GitHub Releases. Set `ESP32AGENT_VERSIONS_URL` to use a custom manifest.
