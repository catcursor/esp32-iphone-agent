// Package app is the esp32agent command line: mobilecli-style commands, each a request to the daemon.
package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/ble"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/config"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/owner"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/textloc"
)

// Version is set at build time (-ldflags "-X .../internal/app.Version=1.2.3").
var Version = "dev"

const usage = `esp32agent: see and drive an iPhone through an ESP32 Agent board.

  ocr install                      install the pinned offline RapidOCR engine and models
  ocr recognize                    read an image from stdin and write OCR boxes as JSON
  pair-usb PORT                    save this board's owner credential for direct Wi-Fi control
  target HOST                      update the network address of the USB-paired board
  photo [--output PATH]            take a photo with the board's camera
  usb PORT wifi status             show the board's Wi-Fi settings over USB
  usb PORT wifi join SSID [--password-stdin] | hotspot | static IPv4 | dhcp
  devices                          show the USB-paired board, or discover nearby Bluetooth boards
  pair | unpair                    manage legacy Bluetooth pairing
  screenshot [--format png|jpeg] [--quality N] [--scale F] [--max-size N] [--output PATH|-]
                                   OCR and geometry in JSON; with --output -, image on stdout and JSON on stderr
  io tap X,Y [--aim] [--count N]   full-screen pixels (use OCR item's tap point); --count 2 double-taps
  io tap-text LABEL [--locate] [--aim]   find a unique visible text label on the full screen
  io tap-grid X,Y --step N [--locate] [--aim]   graph units per grid cell (or NX,NY)
  io longpress X,Y [--duration MS]
  io swipe X1,Y1,X2,Y2
  io text 'TEXT'                   ASCII; Chinese is typed as pinyin
  io keys COMBO                    enter, esc, tab, backspace, space, up, cmd+a, ...
  io button HOME
  screencapture --format h264|mjpeg   video to stdout
  device info                      board status
  net                              the CLI's link (BLE or Wi-Fi) and the board's Wi-Fi
  net via auto|ble                 set the transport preference for a Bluetooth-paired board
  net join                         enter an SSID and password interactively over the authenticated session
  net join SSID --password-stdin    read one password line from standard input
  net hotspot                      the board opens its own hotspot again
  net hotspot-password             show the board's generated hotspot password while in hotspot mode
  net ipv4                         show the board's station IPv4 policy
  net static IPv4 | net dhcp       save a static address or use DHCP on the current station network
  calibrate [--verify]             measure and verify pointer mapping, or only check the saved mapping
  listen                           what the iPhone played in the last 30 s, as words (or why it can't be heard)
  listen on|off                    hear the phone's sound while it mirrors (off by default)
  listen mode phone|local          phone: the ESP32 Agent app's words (default); local: whisper.cpp on this
                                   computer, over Wi-Fi (brew install whisper-cpp, plus a model)
  listen lang CODE|none            local: the language besides English (e.g. zh); default this computer's
  history list | show RUN | new    what was done on the phone (runs; a new one after 10 min idle)
  intent list | show NAME          saved intents (the app's format)
  intent save --name N --prompt P [--hint H ...] [--from-run RUN [--steps 1,3,4]]
  intent delete NAME
  intent export NAME... [--screenshots] [--output FILE]   .intent file for the iOS app
  intent import FILE
  memory list | search TEXT        what the agent remembers (the app's MEMORY.md, same format)
  memory add TEXT [--kind preference|fact|app] [--topic APP]
  memory rm ID
  memory export [--output FILE]    MEMORY.md for the app (Settings › Memory › Import)
  memory import FILE               fold in the app's export: same id or words keep the newer
  report --intent NAME [--screenshots]   prepare a mail to the developer with one intent; you send it
  upgrade [--check] [--force]      install the latest release
  mcp                              MCP server on stdio
  skill install                    agent skill for Claude Code / Codex
  logs [--board] [--save]          the CLI's log; --board the board's; --save one file for support
  daemon [stop]                    the background process (started automatically)
  version

  --device ID or ESP32AGENT_DEVICE when several boards are around.
`

var errUnsupported = errors.New("not supported on ESP32 Agent")

// App runs commands; Out gets the JSON (or raw bytes), Client reaches the daemon.
type App struct {
	In     io.Reader // report's confirmation; os.Stdin when nil
	Out    io.Writer
	Err    io.Writer
	Client *daemon.Client
	Dir    string
}

func Main(args []string) int {
	dir, err := config.Dir()
	a := &App{Out: os.Stdout, Err: os.Stderr, Dir: dir}
	if err != nil {
		return a.print(daemon.Response{Status: "error", Error: err.Error()})
	}
	a.Client = &daemon.Client{Dir: dir, Version: Version}
	return a.Run(args)
}

func (a *App) print(r daemon.Response) int {
	json.NewEncoder(a.Out).Encode(r)
	if r.Status != "ok" {
		return 1
	}
	return 0
}

func (a *App) fail(err error) int {
	return a.print(daemon.Response{Status: "error", Error: err.Error()})
}

// call sends a request and prints the daemon's response as is.
func (a *App) call(req daemon.Request) int {
	line, err := a.Client.Call(req)
	if err != nil {
		return a.fail(err)
	}
	var r daemon.Response
	if json.Unmarshal(line, &r) != nil {
		return a.fail(errors.New("bad response from the daemon"))
	}
	a.Out.Write(line)
	if r.Status != "ok" {
		return 1
	}
	return 0
}

func (a *App) Run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(a.Err, usage)
		return 0
	}
	if args[0] == "--version" {
		args[0] = "version"
	}
	cmd, rest := args[0], args[1:]
	dev := new(string)
	fs := newFS(dev)
	switch cmd {
	case "ocr":
		return a.ocr(rest)
	case "version":
		return a.print(daemon.Response{Status: "ok", Data: map[string]string{"version": Version}})
	case "daemon":
		idle := fs.Duration("idle-timeout", 10*time.Minute, "exit after this long without requests (0: never)")
		pos, err := parse(fs, rest)
		if err != nil {
			return a.fail(err)
		}
		if len(pos) > 0 && pos[0] == "stop" {
			return a.call(daemon.Request{Cmd: "stop"})
		}
		return a.daemon(*idle)
	case "mcp":
		return a.mcp(os.Stdin)
	case "history":
		return a.history(rest)
	case "intent", "intents":
		return a.intent(rest)
	case "memory":
		return a.memory(rest)
	case "report":
		return a.report(rest)
	case "logs":
		return a.logs(rest)
	case "upgrade":
		return a.upgrade(rest)
	case "skill":
		if len(rest) == 0 || rest[0] != "install" {
			return a.fail(errors.New("usage: esp32agent skill install"))
		}
		return a.skillInstall()
	case "calibrate":
		verify := fs.Bool("verify", false, "check the saved pointer mapping without remeasuring")
		pos, err := parse(fs, rest)
		if err != nil {
			return a.fail(err)
		}
		if len(pos) != 0 {
			return a.fail(errors.New("usage: esp32agent calibrate [--verify]"))
		}
		return a.call(daemon.Request{Cmd: cmd, Device: *dev, Verify: *verify})
	case "devices", "pair", "unpair":
		if _, err := parse(fs, rest); err != nil {
			return a.fail(err)
		}
		return a.call(daemon.Request{Cmd: cmd, Device: *dev})
	case "device":
		pos, err := parse(fs, rest)
		if err != nil {
			return a.fail(err)
		}
		if len(pos) >= 2 && pos[0] == "send" { // undocumented: one raw board command, for debugging
			return a.call(daemon.Request{Cmd: "send", Device: *dev, Text: strings.Join(pos[1:], " ")})
		}
		if len(pos) == 0 || pos[0] != "info" {
			return a.fail(errUnsupported)
		}
		return a.call(daemon.Request{Cmd: "info", Device: *dev})
	case "net":
		passwordStdin := fs.Bool("password-stdin", false, "read the Wi-Fi password from stdin")
		pos, err := parse(fs, rest)
		if err != nil {
			return a.fail(err)
		}
		if *passwordStdin {
			if len(pos) != 2 || pos[0] != "join" || pos[1] == "" {
				return a.fail(errors.New("usage: net join SSID --password-stdin"))
			}
			return a.netJoin(*dev, pos[1])
		}
		if len(pos) == 1 && pos[0] == "join" {
			return a.netJoin(*dev, "")
		}
		text := strings.Join(pos, " ")
		if len(pos) >= 2 && pos[0] == "join" { // SSIDs and passwords may hold spaces
			text = "join " + pos[1] + "\x00" + strings.Join(pos[2:], " ")
		}
		return a.call(daemon.Request{Cmd: "net", Device: *dev, Text: text})
	case "listen":
		pos, err := parse(fs, rest)
		if err != nil {
			return a.fail(err)
		}
		return a.call(daemon.Request{Cmd: "listen", Device: *dev, Text: strings.Join(pos, " ")})
	case "screenshot":
		return a.screenshot(fs, dev, rest)
	case "screencapture":
		format := fs.String("format", "mjpeg", "h264 or mjpeg")
		quality := fs.Int("quality", 80, "JPEG quality (mjpeg)")
		if _, err := parse(fs, rest); err != nil {
			return a.fail(err)
		}
		line, err := a.Client.Stream(daemon.Request{Cmd: "screencapture", Device: *dev, Format: *format, Quality: *quality}, a.Out)
		if err != nil {
			return a.fail(err)
		}
		var r daemon.Response
		if json.Unmarshal(line, &r) == nil && r.Status != "ok" {
			return a.print(r)
		}
		return 0
	case "io":
		return a.io(fs, dev, rest)
	}
	return a.fail(errUnsupported)
}

func newFS(dev *string) *flag.FlagSet {
	fs := flag.NewFlagSet("esp32agent", flag.ContinueOnError)
	fs.StringVar(dev, "device", os.Getenv("ESP32AGENT_DEVICE"), "board id")
	return fs
}

func (a *App) io(fs *flag.FlagSet, dev *string, args []string) int {
	aim := fs.Bool("aim", false, "find the pointer on screen and correct a miss before tapping")
	locate := fs.Bool("locate", false, "return the target in full-size screenshot pixels without tapping")
	step := fs.String("step", "", "graph units per grid cell on both axes, or separate X,Y steps")
	ms := fs.Int("duration", 1000, "long press, ms")
	count := fs.Int("count", 1, "taps in a row at the point (2: double tap)")
	pos, err := parse(fs, args)
	if err != nil {
		return a.fail(err)
	}
	if len(pos) == 0 {
		return a.fail(errors.New("usage: esp32agent io tap|tap-text|tap-grid|longpress|swipe|text|keys|button ..."))
	}
	sub, arg := pos[0], strings.Join(pos[1:], " ")
	req := daemon.Request{Cmd: sub, Device: *dev, Aim: *aim, Ms: *ms, Count: *count, Text: arg}
	if *locate && sub != "tap-text" && sub != "tap-grid" {
		return a.fail(errors.New("--locate is available for tap-text and tap-grid"))
	}
	if *step != "" && sub != "tap-grid" {
		return a.fail(errors.New("--step is available for tap-grid"))
	}
	switch sub {
	case "tap-text":
		if strings.TrimSpace(arg) == "" {
			return a.fail(errors.New("usage: esp32agent io tap-text LABEL [--locate] [--aim]"))
		}
		req.Locate = *locate
	case "tap-grid":
		if len(pos) != 2 || *step == "" {
			return a.fail(errors.New("usage: esp32agent io tap-grid X,Y --step N (or NX,NY) [--locate] [--aim]"))
		}
		if req.Pts, err = coords(pos[1], 2); err != nil {
			return a.fail(err)
		}
		units := *step
		if !strings.Contains(units, ",") {
			units += "," + units
		}
		if req.Step, err = coords(units, 2); err != nil {
			return a.fail(err)
		}
		if err = req.CheckGrid(); err != nil {
			return a.fail(err)
		}
		req.Locate, req.Text = *locate, ""
	case "tap", "longpress", "swipe":
		n := 2
		if sub == "swipe" {
			n = 4
		}
		if req.Pts, err = coords(arg, n); err != nil {
			return a.fail(err)
		}
	case "text", "keys", "button":
		if len(pos) < 2 {
			return a.fail(fmt.Errorf("usage: esp32agent io %s VALUE", sub))
		}
	default:
		return a.fail(errUnsupported)
	}
	return a.call(req)
}

func (a *App) screenshot(fs *flag.FlagSet, dev *string, args []string) int {
	format := fs.String("format", "png", "png or jpeg")
	quality := fs.Int("quality", 90, "JPEG quality 1-100")
	scale := fs.Float64("scale", 1, "0-1")
	maxSize := fs.Int("max-size", 0, "max(width, height) in pixels")
	output := fs.String("output", "", "file, directory/, or - for stdout")
	if _, err := parse(fs, args); err != nil {
		return a.fail(err)
	}
	if *format == "jpg" {
		*format = "jpeg"
	}
	line, err := a.Client.Call(daemon.Request{Cmd: "screenshot", Device: *dev, Format: *format, Quality: *quality,
		Scale: *scale, MaxSize: *maxSize})
	if err != nil {
		return a.fail(err)
	}
	var r struct {
		daemon.Response
		Data daemon.Screenshot `json:"data"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return a.fail(err)
	}
	if r.Status != "ok" {
		return a.print(r.Response)
	}
	return a.writeScreenshot(r.Data, *output)
}

func (a *App) writeScreenshot(shot daemon.Screenshot, output string) int {
	img, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return a.fail(err)
	}
	shot.Data = ""
	if shot.OCR.Status == "" {
		// Older daemons can still supply the image but cannot provide its OCR.
		shot.OCR = textloc.Result{Status: "unavailable", Items: []textloc.Item{},
			Error: "running daemon did not return OCR metadata; run esp32agent daemon stop and retry"}
	}
	if output == "-" {
		if _, err := a.Out.Write(img); err != nil {
			if a.Err != nil {
				fmt.Fprintln(a.Err, err)
			}
			return 1
		}
		if a.Err != nil {
			json.NewEncoder(a.Err).Encode(daemon.Response{Status: "ok", Data: shot})
		}
		return 0
	}
	path, err := screenshotPath(output, shot.Device, shot.Format)
	if err != nil {
		return a.fail(err)
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		return a.fail(err)
	}
	shot.FilePath = path
	return a.print(daemon.Response{Status: "ok", Data: shot})
}

// screenshotPath: a file path as given, or screenshot-<device>-<time>.<ext> in a directory (default: cwd).
func screenshotPath(out, device, format string) (string, error) {
	if out != "" && !os.IsPathSeparator(out[len(out)-1]) {
		return filepath.Abs(out)
	}
	ext := "png"
	if format == "jpeg" {
		ext = "jpg"
	}
	base, err := filepath.Abs(filepath.Join(out, fmt.Sprintf("screenshot-%s-%s", device, time.Now().Format("20060102150405"))))
	if err != nil {
		return "", err
	}
	path := base + "." + ext
	for i := 1; ; i++ {
		if _, err := os.Stat(path); err != nil {
			return path, nil
		}
		path = fmt.Sprintf("%s-%d.%s", base, i, ext)
	}
}

func (a *App) daemon(idle time.Duration) int {
	cfg, err := config.Load()
	if err != nil {
		return a.fail(err)
	}
	var profile *owner.Profile
	if daemon.TransportMode() != "ble" && os.Getenv("ESP32AGENT_SIM") == "" {
		profile, err = owner.Load()
		if err != nil && !errors.Is(err, owner.ErrNotPaired) {
			return a.fail(err)
		}
	}
	l, err := daemon.Listen(a.Dir)
	if err != nil {
		return a.fail(err)
	}
	log.SetFlags(log.LstdFlags)
	log.Printf("esp32agent %s daemon on %s", Version, daemon.SocketPath(a.Dir))
	s := &daemon.Server{Transport: ble.Default(), Owner: profile, Config: cfg, Dir: a.Dir, Version: Version, Idle: idle}
	if err := s.Serve(l); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}
