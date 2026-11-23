# Control an iPhone over the LAN with an ESP32

English · [简体中文](README.zh-CN.md)

Connect a XIAO ESP32S3 to an iPhone to let an agent on your computer read the screen, tap, swipe and type. The iPhone sends video to the board through Screen Mirroring. The board sends keyboard and mouse input back over USB. The agent runs the `esp32agent` CLI directly, with no companion iPhone app or MCP server required. A XIAO ESP32S3 Sense can also take photos with its onboard camera.

Development happens on `sense-usb-lan`. Stable CLI binaries are published in this repository's releases. Each successful CLI build on the branch also provides archives for Windows x64, Linux x64 and Apple Silicon macOS on the commit's Actions run. [Stable releases](https://github.com/catcursor/esp32-iphone-agent/releases) · [Branch CLI builds](https://github.com/catcursor/esp32-iphone-agent/actions/workflows/cli.yml?query=branch%3Asense-usb-lan)

## Connect for the first time

1. Flash the XIAO ESP32S3 with this branch's firmware. Install the CLI and FFmpeg on the computer. Run `esp32agent ocr install` to download and verify the pinned RapidOCR engine and models. Debian 12 users can use the system Tesseract package. [Firmware and wiring](docs/usb-lan.md) · [Installation by platform](cli/README.md)
2. Connect the board to the computer with a USB data cable. Replace `PORT` below with its serial port, such as `COM3` on Windows or `/dev/ttyACM0` on Linux. The first command prompts for your 2.4 GHz Wi-Fi password. The second saves the pairing credentials and board address on this computer.

   ```sh
   esp32agent usb PORT wifi join "SSID"
   esp32agent pair-usb PORT
   ```

3. Move the board to the iPhone. Join the board's LAN on the phone, choose **Control Center → Screen Mirroring → ESP32 Agent**, and enable **Settings → Accessibility → Touch → AssistiveTouch**. Run `esp32agent device info` on the computer to check the connection.
4. Open a still page in portrait orientation and run `esp32agent calibrate`. Give your agent the command-line workflow for this fork. [Agent skill](cli/SKILL.md)

## Go from a screenshot to a tap

Run `esp32agent screenshot --output screen.png`. The reply includes the image path in `data.filePath`, the screen dimensions and the OCR results for that frame. Recognition runs synchronously on the computer.

- **Text.** Select an entry in `data.ocr.items[]` and pass its `tap.x,tap.y` directly to `esp32agent io tap X,Y`. Use nearby headings, rows or groups to distinguish repeated labels.
- **Icons and switches.** Read the picture and choose the control's center in screenshot pixels. The origin is the upper left, with X increasing to the right and Y down.
- **After an action.** Capture the screen again. If `data.freshness.status` is `unconfirmed`, keep the connection open and capture again. Choose the next target once the frame is `current`.

## How pointer calibration works

`esp32agent calibrate` uses the straight portions of the left and top edges as references. For each axis, it sends the same total distance in one move and then in five moves. The difference in measured travel divided by four gives the pixel offset per additional move. See `data.strokes.x` and `data.strokes.y` in the reply. The CLI applies the measured gain and offset when sending movement commands.

The computer stores one mapping per **board ID**. If you move the board to another iPhone, run `esp32agent calibrate` again even if the screenshot dimensions match. On Windows the default file is `%APPDATA%\esp32agent\calibration-<board-id>.json`. The first positional gesture in a session checks two pointer locations. A change in screen dimensions triggers a fresh check. Run `esp32agent calibrate --verify` to inspect the requested points, observed positions and errors.

## Other common commands

| Task | Command |
| --- | --- |
| Return to the Home Screen | `esp32agent io button HOME` |
| Swipe | `esp32agent io swipe X1,Y1,X2,Y2` |
| Capture a Sense camera photo | `esp32agent photo --output desk.jpg` |
| Inspect the board's IP settings | `esp32agent net ipv4` |
| Set a static IP or switch to DHCP | `esp32agent net static <IPv4>` or `esp32agent net dhcp` |
| Update the address saved on the computer | `esp32agent target <new-address>` |

After a network change, you can reconnect the board over USB and run `esp32agent usb PORT wifi status` to find its address. [Network configuration and mirroring recovery](docs/usb-lan.md) · [CLI reference](cli/README.md)

## Credits and license

Based on [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent) by Henry Yuan. This fork maintains USB pairing, the LAN CLI, computer-side OCR, pointer calibration and on-demand Sense photos. Please credit the original project in public demos, videos and posts. [NOTICE](NOTICE) · [MIT license](LICENSE)
