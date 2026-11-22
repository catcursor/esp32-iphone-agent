package portableocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestStageArchiveChecksum(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.WriteFile(src, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ESP32AGENT_OCR_ARCHIVE", src)
	sum := sha256.Sum256([]byte("test"))
	a := asset{name: "fixture.zip", bytes: 4, hash: hex.EncodeToString(sum[:])}
	dest := filepath.Join(dir, "archive")
	if err := stageArchive(context.Background(), a, dest); err != nil {
		t.Fatal(err)
	}
	a.hash = strings.Repeat("0", 64)
	if err := stageArchive(context.Background(), a, dest); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}

// Intel macOS runners can verify the complete Apple Silicon distribution and
// model files even though they cannot execute the ARM binary.
func TestAppleSiliconRelease(t *testing.T) {
	if os.Getenv("ESP32AGENT_TEST_APPLE_OCR") != "1" {
		t.Skip("release asset check")
	}
	dir := t.TempDir()
	a := assets["darwin/arm64"]
	archive := filepath.Join(dir, a.name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := stageArchive(ctx, a, archive); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "tar", "-xf", archive, "-C", dir).CombinedOutput(); err != nil {
		t.Fatalf("extract: %s: %v", out, err)
	}
	exe, _, err := paths(filepath.Join(dir, "rapidocr-macos-arm64"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := macho.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Cpu != macho.CpuArm64 {
		t.Fatalf("Apple executable architecture %v", f.Cpu)
	}
}

func TestStartupPort(t *testing.T) {
	for input, want := range map[string]string{
		"INFO: Uvicorn running on http://127.0.0.1:12345 (Press CTRL+C to quit)":                              "12345",
		"\x1b[32mINFO\x1b[0m: Uvicorn running on \x1b[1mhttp://127.0.0.1:57680\x1b[0m (Press CTRL+C to quit)": "57680",
		"Uvicorn running on http://0.0.0.0:9000":                                                              "",
		"Uvicorn running on http://127.0.0.1:99999":                                                           "",
	} {
		if got := startupPort(input); got != want {
			t.Fatalf("%q: got %q, want %q", input, got, want)
		}
	}
}

// Run on native release platforms after installing the pinned OCR package.
func TestInstalledEngine(t *testing.T) {
	if os.Getenv("ESP32AGENT_TEST_RAPIDOCR") != "1" {
		t.Skip("requires the installed offline runtime")
	}
	label := image.NewRGBA(image.Rect(0, 0, 170, 32))
	draw.Draw(label, label.Bounds(), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: label, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13, Dot: fixed.P(10, 21)}
	d.DrawString("Portable OCR 12345")
	large := image.NewRGBA(image.Rect(0, 0, 680, 128))
	xdraw.CatmullRom.Scale(large, large.Bounds(), label, label.Bounds(), draw.Src, nil)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, large); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := Recognize(ctx, imageBytes.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var lines []struct {
		Text  string      `json:"txt"`
		Score float64     `json:"score"`
		Box   [][]float64 `json:"box"`
	}
	if err := json.Unmarshal(result, &lines); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if strings.Contains(l.Text, "12345") && l.Score > 0 && l.Score <= 1 && len(l.Box) == 4 && len(l.Box[0]) == 2 {
			return
		}
	}
	t.Fatalf("expected recognized text, confidence and bounds; got %s", result)
}
