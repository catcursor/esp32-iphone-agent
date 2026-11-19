package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/config"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/owner"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/sensephoto"
)

// A running daemon has selected a board and its credential. The next command
// must start with the newly paired identity or the newly saved target.
func stopDaemon() error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	(&daemon.Client{Dir: dir}).StopRunning()
	return nil
}

func ownerCommand(args []string) (any, error) {
	switch args[0] {
	case "pair-usb":
		if len(args) != 2 {
			return nil, errors.New("usage: esp32agent pair-usb PORT")
		}
		p, err := owner.Pair(args[1])
		if err != nil {
			return nil, err
		}
		defer clear(p.Key)
		return map[string]any{"paired": true, "board": hex.EncodeToString(p.BoardID), "host": p.Host}, stopDaemon()
	case "target":
		if len(args) != 2 {
			return nil, errors.New("usage: esp32agent target HOST")
		}
		if err := owner.SetHost(args[1]); err != nil {
			return nil, err
		}
		return map[string]string{"host": args[1]}, stopDaemon()
	case "photo":
		var path string
		if len(args) == 3 && args[1] == "--output" && args[2] != "" {
			path = args[2]
		} else if len(args) != 1 {
			return nil, errors.New("usage: esp32agent photo [--output PATH]")
		}
		p, err := owner.Load()
		if err != nil {
			return nil, err
		}
		defer clear(p.Key)
		if p.Host == "" {
			return nil, errors.New("no LAN address saved; run esp32agent target HOST")
		}
		key := p.PhotoKey()
		defer clear(key)
		return sensephoto.Capture(p.Host, key, path)
	case "usb":
		return usbWiFi(args[1:])
	}
	return nil, errors.New("unknown owner command")
}

const wifiUsage = "usage: esp32agent usb PORT wifi status | join SSID [--password-stdin] | hotspot | hotspot-password | static IP | dhcp"

func usbWiFi(args []string) (any, error) {
	if len(args) < 3 || args[1] != "wifi" {
		return nil, errors.New(wifiUsage)
	}
	port, op := args[0], args[2]
	var err error
	switch {
	case op == "status" && len(args) == 3:
		return owner.WiFiStatus(port)
	case op == "hotspot-password" && len(args) == 3:
		return owner.WiFiHotspotPassword(port)
	case op == "hotspot" && len(args) == 3:
		err = owner.WiFiHotspot(port)
	case op == "dhcp" && len(args) == 3:
		err = owner.WiFiDHCP(port)
	case op == "static" && len(args) == 4:
		err = owner.WiFiStatic(port, args[3])
	case op == "join" && (len(args) == 4 || len(args) == 5 && args[4] == "--password-stdin"):
		var pass []byte
		pass, err = readPassword(len(args) == 5)
		if err == nil {
			defer clear(pass)
			err = owner.WiFiJoin(port, args[3], string(pass))
		}
	default:
		return nil, errors.New(wifiUsage)
	}
	if err != nil {
		return nil, err
	}
	// A command may be acknowledged before DHCP or the bridge's reconnect.
	return map[string]any{"accepted": true, "operation": op}, nil
}

func readPassword(fromPipe bool) ([]byte, error) {
	if !fromPipe {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return nil, errors.New("use --password-stdin to read a password from a pipe")
		}
		fmt.Fprint(os.Stderr, "Wi-Fi password (empty for an open network): ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return b, err
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("omit --password-stdin for hidden password entry")
	}
	r := bufio.NewReader(io.LimitReader(os.Stdin, 68))
	line, err := r.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) > 64 {
		return nil, errors.New("Wi-Fi password exceeds 64 bytes")
	}
	return []byte(line), nil
}
