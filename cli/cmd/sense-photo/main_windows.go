// sense-photo is the Windows standalone client for the board's camera.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/owner"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/sensephoto"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sense-photo:", err)
		os.Exit(1)
	}
}
func run() error {
	host := flag.String("host", "", "board hostname or IPv4 address; defaults to the saved board")
	output := flag.String("output", "", "new JPEG file (default: Pictures/ESP32/<timestamp>.jpg)")
	port := flag.String("pair-usb", "", "pair this camera-only client over a physical USB serial port")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments; use --help")
	}
	if *port != "" {
		if *output != "" || *host != "" {
			return errors.New("use --pair-usb on its own")
		}
		if err := owner.PairLegacyPhoto(*port); err != nil {
			return err
		}
		fmt.Println("USB photo-only pairing complete.")
		return nil
	}
	key, addr, err := owner.LegacyPhoto()
	if errors.Is(err, os.ErrNotExist) {
		p, profileErr := owner.Load()
		if errors.Is(profileErr, owner.ErrNotPaired) {
			return errors.New("no photo-only or owner credential: pair over USB first")
		}
		if profileErr != nil {
			return profileErr
		}
		key, addr, err = p.PhotoKey(), p.Host, nil
		clear(p.Key)
	}
	if err != nil {
		return err
	}
	defer clear(key)
	if *host != "" {
		addr = *host
	}
	res, err := sensephoto.Capture(addr, key, *output)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%d x %d)\n", res.FilePath, res.Width, res.Height)
	return nil
}
