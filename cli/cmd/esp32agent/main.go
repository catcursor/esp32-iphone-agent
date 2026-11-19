// esp32agent operates a real iPhone using a paired ESP32 Agent board.
package main

import (
	"encoding/json"
	"os"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/app"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "pair-usb", "target", "photo", "usb":
			result, err := ownerCommand(args)
			if err != nil {
				json.NewEncoder(os.Stdout).Encode(daemon.Response{Status: "error", Error: err.Error()})
				os.Exit(1)
			}
			json.NewEncoder(os.Stdout).Encode(daemon.Response{Status: "ok", Data: result})
			return
		}
	}
	os.Exit(app.Main(args))
}
