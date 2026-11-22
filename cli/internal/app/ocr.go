package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/portableocr"
)

func (a *App) ocr(args []string) int {
	if len(args) != 1 {
		return a.fail(errors.New("usage: esp32agent ocr install | recognize"))
	}
	switch args[0] {
	case "install":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		exe, err := portableocr.Install(ctx)
		if errors.Is(err, portableocr.ErrIncompatibleLibc) {
			_, lookupErr := exec.LookPath("tesseract")
			return a.print(daemon.Response{Status: "ok", Data: map[string]any{"installed": false, "reason": err.Error(), "fallback": "tesseract", "tesseract_on_path": lookupErr == nil}})
		}
		if err != nil {
			return a.fail(err)
		}
		return a.print(daemon.Response{Status: "ok", Data: map[string]any{"installed": true, "executable": exe}})
	case "recognize":
		input := a.In
		if input == nil {
			input = os.Stdin
		}
		const maxImage = 32 << 20
		image, err := io.ReadAll(io.LimitReader(input, maxImage+1))
		if err == nil && (len(image) == 0 || len(image) > maxImage) {
			err = errors.New("expected one image of up to 32 MiB on stdin")
		}
		if err == nil {
			var results []byte
			results, err = portableocr.Recognize(context.Background(), image)
			if err == nil {
				_, err = fmt.Fprintln(a.Out, string(results))
			}
		}
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		return 0
	default:
		return a.fail(errors.New("usage: esp32agent ocr install | recognize"))
	}
}
