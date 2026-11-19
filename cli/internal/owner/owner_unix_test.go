//go:build darwin || linux

package owner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnixCredentialPermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ESP32AGENT_HOME", root)
	if _, err := Load(); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("missing profile: %v", err)
	}
	p := Profile{ClientID: make([]byte, 16), BoardID: make([]byte, 6), Key: bytes.Repeat([]byte{7}, 32), Host: "192.0.2.7"}
	if err := save(&p); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "credentials")
	path := filepath.Join(dir, "owner.json")
	for name, mode := range map[string]os.FileMode{dir: 0700, path: 0600} {
		s, err := os.Stat(name)
		if err != nil || s.Mode().Perm() != mode {
			t.Fatalf("%s: want %o, got %v (%v)", name, mode, s, err)
		}
	}
	if err := SetHost("camera.local"); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(); err != nil || got.Host != "camera.local" || !bytes.Equal(got.Key, p.Key) {
		t.Fatalf("profile did not survive target change: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("accepted publicly readable profile")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path+".old", 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".old", path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("followed a symlink to a credential")
	}
}

func TestPairAndNetworkSettingsOverPTY(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 needed for the simulated serial port")
	}
	t.Setenv("ESP32AGENT_HOME", t.TempDir())
	// Python's standard library provides an openpty on both Darwin and Linux.
	// Keep the slave open until the client has received the last reply.
	const peer = `
import json, os, sys
master, slave = os.openpty()
print(os.ttyname(slave), flush=True)
port = os.fdopen(master, "r+b", buffering=0)
for command, reply in [
    ("owner-pair", json.dumps(dict(id="11"*16, board="22"*6, key="33"*32, ip="192.0.2.10"))),
    ("netstatus", '{"mode":"sta","ssid":"test","ip":"192.0.2.10"}'),
    ("ipv4", 'ok {"policy":"static","address":"192.0.2.10"}'),
    ("ipv4 dhcp", "ok"),
]:
    if port.readline() != ("0 " + command + "\n").encode(): sys.exit(2)
    port.write(("board log\r\n0 " + reply + "\r\n").encode())
sys.stdin.read()
`
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-u", "-c", peer)
	cmd.Stderr = os.Stderr
	control, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		control.Close()
		cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Wait()
		}
	}()
	device, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimSpace(device)
	p, err := Pair(port)
	if err != nil || p.Host != "192.0.2.10" {
		t.Fatalf("pair: %v", err)
	}
	status, err := WiFiStatus(port)
	if err != nil || status.IPv4 == nil || status.IPv4.Policy != "static" {
		t.Fatalf("status: %v", err)
	}
	if err := WiFiDHCP(port); err != nil {
		t.Fatal(err)
	}
	control.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
