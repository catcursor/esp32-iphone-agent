package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/textloc"
)

func TestWriteScreenshotMetadataAndImage(t *testing.T) {
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0a, 0xff}
	shot := daemon.Screenshot{
		Format: "png", Data: base64.StdEncoding.EncodeToString(imageBytes), Width: 20, Height: 30,
		SourceWidth: 40, SourceHeight: 60, SourceRect: textloc.Box{Width: 40, Height: 60},
		OCR: textloc.Result{Status: "ok", Engine: "test", Items: []textloc.Item{{Text: "保存"}}},
	}
	for _, output := range []string{filepath.Join(t.TempDir(), "out.png"), "-"} {
		var stdout, stderr bytes.Buffer
		app := App{Out: &stdout, Err: &stderr}
		if app.writeScreenshot(shot, output) != 0 {
			t.Fatalf("screenshot failed: %s, %s", stdout.String(), stderr.String())
		}
		var img, metadata []byte
		if output == "-" {
			img, metadata = stdout.Bytes(), stderr.Bytes()
		} else {
			var err error
			img, err = os.ReadFile(output)
			if err != nil || stderr.Len() != 0 {
				t.Fatalf("screenshot file output: %v, %s", err, stderr.String())
			}
			metadata = stdout.Bytes()
		}
		var result struct {
			Status string
			Data   daemon.Screenshot
		}
		if !bytes.Equal(img, imageBytes) || json.Unmarshal(metadata, &result) != nil || result.Status != "ok" ||
			result.Data.Data != "" || len(result.Data.OCR.Items) != 1 ||
			result.Data.OCR.Items[0].Text != "保存" || result.Data.SourceRect != shot.SourceRect {
			t.Fatalf("lost image or OCR metadata: %v, %s", img, metadata)
		}
	}
}
