// Package portableocr runs the offline RapidOCR distribution installed by esp32agent.
package portableocr

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("portable OCR is not installed; run esp32agent ocr install")

// Uvicorn can colorize the URL even with stderr redirected on Windows.
func startupPort(line string) string {
	if !strings.Contains(line, "Uvicorn running on ") {
		return ""
	}
	_, tail, ok := strings.Cut(line, "http://127.0.0.1:")
	if !ok {
		return ""
	}
	end := strings.IndexFunc(tail, func(r rune) bool { return r < '0' || r > '9' })
	if end >= 0 {
		tail = tail[:end]
	}
	n, err := strconv.ParseUint(tail, 10, 16)
	if err != nil || n == 0 {
		return ""
	}
	return tail
}

const version = "0.1.0"

type asset struct {
	name, hash string
	bytes      int64
}

var assets = map[string]asset{
	"windows/amd64": {"rapidocr-windows-x86_64.zip", "d61866e396de191d25a2265aaf0fc7f613fc76f87bb2063c57e81a1c8f017adc", 103491016},
	"linux/amd64":   {"rapidocr-linux-x86_64.tar.gz", "72fa642543941eb68acf113d8cc03a67f3ed71241d25b7b286702212f9f5152a", 189946277},
	"darwin/arm64":  {"rapidocr-macos-arm64.tar.gz", "9e690ba00a61ed13224bcf43c5216fe0dd37785db418a979a31ee3d746f7412a", 107663252},
}

func release() (asset, string, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	a, ok := assets[platform]
	if !ok {
		return a, "", fmt.Errorf("%w: no release for %s", ErrUnavailable, platform)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return a, "", err
	}
	return a, filepath.Join(dir, "esp32agent", "rapidocr", version, strings.TrimSuffix(strings.TrimSuffix(a.name, ".tar.gz"), ".zip")), nil
}

func paths(dir string) (exe, models string, err error) {
	name := "rapidocr"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe = filepath.Join(dir, name)
	models = filepath.Join(dir, "_internal", "rapidocr", "models")
	st, err := os.Stat(exe)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrUnavailable
	}
	if err != nil {
		return "", "", err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return "", "", fmt.Errorf("%w: invalid executable", ErrUnavailable)
	}
	// RapidOCR's upstream loader downloads missing or invalid model files.
	// Fail before launching it if any bundled defaults are unavailable.
	for name, hash := range modelHashes {
		f, err := os.Open(filepath.Join(models, name))
		if errors.Is(err, os.ErrNotExist) {
			return "", "", ErrUnavailable
		}
		if err != nil {
			return "", "", err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		if err := errors.Join(copyErr, f.Close()); err != nil {
			return "", "", err
		}
		if hex.EncodeToString(h.Sum(nil)) != hash {
			return "", "", fmt.Errorf("%w: invalid %s", ErrUnavailable, name)
		}
	}
	return exe, models, nil
}

var modelHashes = map[string]string{
	"ch_PP-OCRv4_det_mobile.onnx":          "d2a7720d45a54257208b1e13e36a8479894cb74155a5efe29462512d42f49da9",
	"ch_ppocr_mobile_v2.0_cls_mobile.onnx": "e47acedf663230f8863ff1ab0e64dd2d82b838fceb5957146dab185a89d6215c",
	"ch_PP-OCRv4_rec_mobile.onnx":          "48fc40f24f6d2a207a2b1091d3437eb3cc3eb6b676dc3ef9c37384005483683b",
}

// Recognize returns RapidOCR's JSON records with text, confidence, and the four box corners.
// It never installs software. Only the private child receives the image on a loopback port.
func Recognize(ctx context.Context, image []byte) ([]byte, error) {
	_, dir, err := release()
	if err != nil {
		return nil, err
	}
	exe, models, err := paths(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "serve", "--host", "127.0.0.1", "--port", "0")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "RAPIDOCR_MODEL_DIR="+models, "NO_COLOR=1", "UVICORN_USE_COLORS=false")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { cancel(); cmd.Wait() }()
	portCh := make(chan string, 1)
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			if port := startupPort(s.Text()); port != "" {
				select {
				case portCh <- port:
				default:
				}
			}
		}
	}()
	var port string
	select {
	case port = <-portCh:
	case <-logDone:
		return nil, errors.New("local RapidOCR process exited before startup")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	payload, err := json.Marshal(struct {
		Image []byte `json:"image"`
	}{image})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+net.JoinHostPort("127.0.0.1", port)+"/api/ocr", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local RapidOCR returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Results) == 0 || out.Results[0] != '[' {
		return nil, errors.New("local RapidOCR returned invalid results")
	}
	return out.Results, nil
}
