//go:build windows || darwin || linux

package sensephoto

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Result struct {
	FilePath string `json:"filePath"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

func Capture(host string, key []byte, output string) (Result, error) {
	if host == "" {
		return Result{}, errors.New("no board address saved; specify --host or pair again over USB")
	}
	if output == "" {
		dir, err := picturesDir()
		if err != nil {
			return Result{}, err
		}
		output = filepath.Join(dir, "ESP32", time.Now().Format("2006-01-02_15-04-05.000000000")+".jpg")
	}
	path, err := filepath.Abs(output)
	if err != nil {
		return Result{}, err
	}
	if _, err = os.Lstat(path); err == nil {
		return Result{}, fmt.Errorf("output already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	addr := host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "7300")
	}
	conn, err := dialPhoto(addr)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return Result{}, err
	}
	image, err := capture(conn, key)
	if err != nil {
		return Result{}, err
	}
	if err = saveJPEG(path, image.data); err != nil {
		return Result{}, err
	}
	return Result{path, image.width, image.height}, nil
}

func saveJPEG(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sense-photo-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return publishPhoto(tmp.Name(), path)
}
