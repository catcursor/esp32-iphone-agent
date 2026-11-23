---
name: esp32agent
description: Control a real iPhone through a USB-paired ESP32 board on the LAN. Use for reading the mirrored screen and its OCR boxes, tapping, typing, swiping, or taking an on-demand Sense camera photo.
---

# esp32agent

Run the CLI on the computer. For initial setup plug the board into the computer and run `esp32agent pair-usb PORT`. During use, plug it into the iPhone, mirror the screen to ESP32 Agent and enable AssistiveTouch. Check the connection with `esp32agent device info`.

## Use the screen and its text

At deployment, use `esp32agent ocr install` to provision the pinned [RapidOCR runtime and models](https://github.com/AwesomeDog/RapidOCR-CLI/releases/tag/v0.1.0) on this computer. Screenshots use this local installation.

1. Run `esp32agent screenshot`. Open `data.filePath` and read `data.ocr` in the same reply. Local OCR runs synchronously on each screenshot.
2. Decide one action from the picture and the text. Each OCR item includes the text, bounding box, center and a ready-to-use `tap` point. Pick among repeated labels using adjacent text, icons and rows. On the Home Screen, use the app name to identify its icon and tap the icon itself. Locate switches and diagrams visually, including when OCR returns no usable text.
3. Use `esp32agent io tap X,Y` at the chosen full-screen point, measured from the upper left. Use the OCR item's `tap.x,tap.y` as supplied. For other actions use `io longpress X,Y --duration 1500`, `io swipe X1,Y1,X2,Y2`, `io text 'hello'`, `io keys enter` or `io button HOME`.
4. Take a fresh screenshot after each action and check what changed. If `data.freshness.status` is `unconfirmed`, read the reason. The connection may still be replaying earlier video or waiting for a frame after the last input. Keep the session open and capture again before using its coordinates.

## Calibrate the pointer

On first use or when moving the board to another iPhone, open a still portrait screen, keep mirroring active with the AssistiveTouch pointer visible, and run `esp32agent calibrate`. The computer stores one mapping per board ID. Recalibrate after a phone swap even when both screenshots have identical dimensions.

The CLI uses the left and top edges as references and compares one move with five moves over the same distance on each axis. The `data.strokes.x.offset_px` and `data.strokes.y.offset_px` fields report the extra travel per additional move. The CLI applies the correction internally. Pass the current OCR item's `tap.x,tap.y` directly.

The first positional gesture in a session checks two pointer locations. A change in screen dimensions triggers a fresh check. Run `esp32agent calibrate --verify` on a still screen for the requested and observed positions and their errors. It only moves the pointer. Add `--aim` to a tap for visual correction at that point.

For a single known, unique string use `esp32agent io tap-text "Continue" --locate`. For an evenly spaced Cartesian grid with visible zero axes use `esp32agent io tap-grid -25,20 --step 5 --locate`. Omitting `--locate` clicks. Use `esp32agent photo` when the user requests an image from the physical Sense camera.

## Typing
- The board types printable ASCII only, one key per character.
- Chinese is typed as pinyin: `io text '拿铁'` types `natie` (the reply says `typed_as`). With the iPhone's Chinese (pinyin)
  keyboard active, commit the first candidate with `io keys space`, or screenshot and tap the right candidate.
  Many search boxes accept the plain pinyin as well.
- The board is a hardware keyboard, so iOS may hide the on-screen keyboard; the text field still takes the keys.
- Tap the text field first so it has focus; `io keys cmd+a` then `io keys backspace` clears it.

## Save what worked
After a task succeeded, save it so the next run is faster:
1. `esp32agent history show latest` (MCP: `history` with action show): the run's steps in 0-1000 screen units,
   numbered, each with the screenshot it was done on. Every action and screenshot is recorded by itself; a new run
   starts after 10 minutes idle, or with `esp32agent history new` before a new task.
2. Pick the steps that did the job (skip detours, misses and retries).
3. `esp32agent intent save --name 'Alarm 7:00' --prompt 'Set an alarm for 7:00 AM every day' --hint 'Type the time
   instead of scrolling the wheel' --from-run RUN --steps 2,3,5` (MCP: `intent_save` with name, prompt, hints,
   from_run, steps). One short, clear instruction as the prompt and at most 6 concrete hints. Only replayable moves
   can be steps: tap, swipe, type, key, home. The same name replaces the old intent.

## Run a saved intent
1. `esp32agent intent list`, then `esp32agent intent show NAME` (MCP: `intent_list`, `intent_show`): the prompt,
   hints and recorded steps, each with its reference screen (a JPEG path).
2. Follow the steps as references, not blindly: coordinates are 0-1000 units of the screen, so convert them to
   pixels of your screenshot (x * width / 1000, y * height / 1000).
3. Take a screenshot after each step and check it looks like the next step's reference screen.
4. On a mismatch (another screen, a popup, a changed layout) drop the recorded steps and go on with the normal loop,
   using the prompt and hints.

## Memory
- Before a task, `esp32agent memory search 'TASK'` (MCP: `memory_recall`): the user's preferences and how their apps
  work, the same entries the iOS app uses.
- When the user says to remember something, `esp32agent memory add 'TEXT' --kind preference` (MCP: `memory_add`).
  Only the user's own words; never passwords, codes or numbers off the screen.

## Errors to act on
- `busy: the app is using the phone`: the iOS app is driving right now. Wait and retry later; don't fight it.
- `screen mirroring is off`: ask the user to start Screen Mirroring to ESP32 Agent.
- When the board reports `ap=streaming`, `fz=1` and `kfr=1`, keep the same connection while the board requests a fresh keyframe. Capture again and check that freshness is current. Restart Screen Mirroring if recovery is unavailable or the sender continues to withhold a keyframe.
- `not paired`: connect the board to the computer and use `esp32agent pair-usb PORT`.
- `phone usb asleep`: the phone is locked; ask the user to unlock it.
- `not supported on ESP32 Agent`: there is no app list, UI tree, file access or webview; work from screenshots.

## Limits
- Check the current mirroring and frozen-video status if the picture stops changing.
- Only HOME as a button; no volume, power, or app install/launch (open apps from the Home Screen or Spotlight:
  `io button HOME`, swipe down in the middle, `io text 'Settings'`, `io keys enter`).
