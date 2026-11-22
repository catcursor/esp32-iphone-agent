package portableocr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Install stages and verifies a complete upstream distribution before publishing
// the directory. Both the interpreter and the three default ONNX models are included.
func Install(ctx context.Context) (string, error) {
	a, dir, err := release()
	if err != nil {
		return "", err
	}
	if exe, _, err := paths(dir); err == nil {
		return exe, nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	archive := filepath.Join(temp, a.name)
	if err := stageArchive(ctx, a, archive); err != nil {
		return "", err
	}
	// Use the system's native tar to preserve Unix shared-library symlinks and
	// macOS framework bundles. Windows 10+ ships libarchive tar with ZIP support.
	tar := "tar"
	if runtime.GOOS == "windows" {
		// Git Bash can put GNU tar ahead of Windows' ZIP-capable bsdtar.
		tar = filepath.Join(os.Getenv("SystemRoot"), "System32", "tar.exe")
	}
	cmd := exec.CommandContext(ctx, tar, "-xf", archive, "-C", temp)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extracting RapidOCR: %w: %s", err, output)
	}
	extracted := filepath.Join(temp, filepath.Base(dir))
	if _, _, err := paths(extracted); err != nil {
		return "", fmt.Errorf("incomplete RapidOCR release: %w", err)
	}
	if err := os.Rename(extracted, dir); err != nil {
		// Another installer may have finished while the archive was downloading.
		if exe, _, already := paths(dir); already == nil {
			return exe, nil
		}
		// Permit repair of an interrupted or damaged installation. Only replace
		// our platform/version directory, restoring it if publishing fails.
		old := filepath.Join(temp, "previous")
		if moveErr := os.Rename(dir, old); moveErr != nil {
			return "", err
		}
		if publishErr := os.Rename(extracted, dir); publishErr != nil {
			if restoreErr := os.Rename(old, dir); restoreErr != nil {
				return "", errors.Join(publishErr, restoreErr)
			}
			return "", publishErr
		}
	}
	exe, _, err := paths(dir)
	return exe, err
}

func stageArchive(ctx context.Context, a asset, dest string) error {
	var src io.ReadCloser
	if local := os.Getenv("ESP32AGENT_OCR_ARCHIVE"); local != "" {
		f, err := os.Open(local)
		if err != nil {
			return err
		}
		src = f
	} else {
		url := "https://github.com/AwesomeDog/RapidOCR-CLI/releases/download/v" + version + "/" + a.name
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("downloading %s: %s", a.name, resp.Status)
		}
		src = resp.Body
	}
	defer src.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(src, a.bytes+1))
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return err
	}
	if n != a.bytes || hex.EncodeToString(hash.Sum(nil)) != a.hash {
		return fmt.Errorf("%s: release size or SHA-256 mismatch", a.name)
	}
	return nil
}
