//go:build !windows

package textloc

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/portableocr"
)

const nativeEngine = "tesseract"

func recognize(ctx context.Context, path, label string) ([][]word, error) {
	bin, err := exec.LookPath("tesseract")
	if err != nil {
		return nil, fmt.Errorf("%w: install the portable OCR bundle or Tesseract with OCR language data", portableocr.ErrUnavailable)
	}
	var args []string
	if script := scriptOf(label); script != "" || label == "" {
		raw, err := exec.CommandContext(ctx, bin, "--list-langs").Output()
		if err != nil {
			return nil, fmt.Errorf("listing Tesseract languages: %w", err)
		}
		available := strings.Fields(string(raw))
		candidates := map[string][]string{"zh": {"chi_sim", "chi_tra"}, "ja": {"jpn"}, "ko": {"kor"}}[script]
		if label == "" {
			// A full screenshot has no target label from which to infer the script.
			candidates = []string{"chi_sim", "chi_tra", "jpn", "kor"}
		}
		var langs []string
		for _, lang := range candidates {
			if slices.Contains(available, lang) {
				langs = append(langs, lang)
			}
		}
		if len(langs) == 0 && label != "" {
			return nil, fmt.Errorf("%w: install Tesseract OCR language data for %s", portableocr.ErrUnavailable, strings.Join(candidates, " or "))
		}
		if slices.Contains(available, "eng") {
			langs = append(langs, "eng")
		}
		if len(langs) > 0 {
			args = []string{"-l", strings.Join(langs, "+")}
		}
	}
	args = append([]string{path, "stdout", "--psm", "11"}, args...)
	out, err := exec.CommandContext(ctx, bin, append(args, "tsv")...).Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("Tesseract: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("Tesseract: %w", err)
	}
	return parseTSV(string(out))
}
