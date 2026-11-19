//go:build darwin || linux

package sensephoto

import (
	"os"
	"path/filepath"
)

func picturesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Pictures"), nil
}
