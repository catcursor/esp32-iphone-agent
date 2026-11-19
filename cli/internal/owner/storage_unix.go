//go:build darwin || linux

package owner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Unix profiles rely on the account's filesystem permissions. Windows uses DPAPI.
const ownerFile = "owner.json"
const credentialSuffix = ""

func credentialsDir(create bool) (string, error) {
	root := os.Getenv("ESP32AGENT_HOME")
	if root == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(base, "esp32agent")
	}
	dir := filepath.Join(root, "credentials")
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || !ownedAndPrivate(info) {
		return "", fmt.Errorf("credential directory must belong to the current user and use mode 0700: %s", dir)
	}
	return dir, nil
}

func ownedAndPrivate(info os.FileInfo) bool {
	stat := info.Sys().(*syscall.Stat_t)
	return stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}

func writeProtected(name string, data []byte) error {
	if len(data) == 0 || len(data) > 16*1024 {
		return errors.New("invalid credential size")
	}
	dir, err := credentialsDir(true)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".pair-*") // 0600, same filesystem as the destination
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

func readProtected(name string) ([]byte, error) {
	dir, err := credentialsDir(false)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedAndPrivate(info) {
		return nil, errors.New("credential file must be a regular file owned by the current user with mode 0600")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(data) == 0 || len(data) > 16*1024 {
		clear(data)
		if err == nil {
			err = errors.New("invalid credential size")
		}
		return nil, err
	}
	return data, nil
}
