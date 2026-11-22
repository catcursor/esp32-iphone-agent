# ESP32 iPhone Agent over LAN

English · [简体中文](README.zh-CN.md)

Control a real iPhone from a computer with a Seeed Studio XIAO ESP32S3. The iPhone sends its screen to the board via Screen Mirroring and receives mouse and keyboard input via USB. Your agent runs on the computer and calls `esp32agent` over the LAN. This workflow uses built-in iPhone features and requires no companion app. The Sense camera can also take a photo on request.

## Set up

1. Flash the board with the firmware from this fork’s `sense-usb-lan` branch. Install the CLI for Windows x64, Linux x64 or Apple Silicon macOS and make FFmpeg available to the CLI. See the [setup guide](docs/usb-lan.md), [CLI installation instructions](cli/README.md) and [releases](https://github.com/catcursor/esp32-iphone-agent/releases).
2. Connect the board to the computer with a USB data cable. Run `esp32agent usb PORT wifi join "SSID"` to configure Wi-Fi, then `esp32agent pair-usb PORT` to save the board’s identity and LAN target. Replace `PORT` with your serial port.
3. Move the board to the iPhone. Join the same LAN on the phone, select **Control Center → Screen Mirroring → ESP32 Agent**, and enable **Settings → Accessibility → Touch → AssistiveTouch**. On the computer, run `esp32agent device info`.
4. Give your agent the [iphone-lan skill](skills/iphone-lan/SKILL.md). It uses the command line directly.

## Screenshots, OCR and taps

Run `esp32agent screenshot --output screen.png` to get the screen image and the text found in that frame. The computer runs OCR synchronously. The deployment step fetches the pinned [RapidOCR runtime and models](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0). Screenshots are processed locally.

The agent sees the picture and OCR results together. It can choose a text box using nearby labels, locate an icon visually, or disregard the OCR result entirely. Give `esp32agent io tap X,Y` a point in full-screen pixels measured from the upper left. After each action, take a new screenshot to check the result.

Before the first positional gesture in a control session the CLI checks the pointer against the mirrored screen by moving it to two reference points. Each point must land within 1.5% of the mirrored width horizontally and 1.5% of the height vertically. The CLI repeats the check when the screen dimensions change. To check a saved mapping without clicking, run `esp32agent calibrate --verify`. To measure again, run `esp32agent calibrate`. The measured error describes where the pointer landed. Whether the agent selected the intended control is checked using the screen after the action.

Use `esp32agent photo --output desk.jpg` for a still image from the Sense camera. See [network settings and USB recovery](docs/usb-lan.md) for static IP and DHCP.

## Credits

Based on [ESP32 iPhone Agent](https://github.com/onehumanbeing/esp32-iphone-agent) by Henry Yuan. The upstream project documents its iOS app, Bluetooth workflow and original hardware design. Attribution for redistribution and public demos is in [NOTICE](NOTICE) and the [MIT license](LICENSE).
