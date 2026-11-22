package textloc

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/portableocr"
)

const nativeEngine = "windows-ocr"

//go:embed recognize.ps1
var windowsOCR string

func recognize(ctx context.Context, path, label string) ([][]word, error) {
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", windowsOCR)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// These are data, not part of the command string.
	cmd.Env = append(cmd.Environ(), "ESP32AGENT_OCR_INPUT="+path, "ESP32AGENT_OCR_SCRIPT="+scriptOf(label))
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) != 0 {
			if exit.ExitCode() == 3 {
				return nil, fmt.Errorf("%w: %s", portableocr.ErrUnavailable, strings.TrimSpace(string(exit.Stderr)))
			}
			return nil, fmt.Errorf("Windows OCR: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: Windows PowerShell is missing", portableocr.ErrUnavailable)
		}
		return nil, fmt.Errorf("Windows OCR: %w", err)
	}
	var lines [][]word
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, fmt.Errorf("decoding Windows OCR words: %w", err)
	}
	return lines, nil
}
