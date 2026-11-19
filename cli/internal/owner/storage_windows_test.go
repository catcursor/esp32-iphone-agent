package owner

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestOwnerProfileIsProtectedAndReplaced(t *testing.T) {
	t.Setenv("ESP32AGENT_HOME", t.TempDir())
	if _, err := Load(); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("new profile: %v", err)
	}
	p := Profile{ClientID: bytes.Repeat([]byte{12}, 16), BoardID: bytes.Repeat([]byte{13}, 6), Key: bytes.Repeat([]byte{14}, 32), Host: "192.0.2.100"}
	if err := save(&p); err != nil {
		t.Fatal(err)
	}
	file, _ := protectedPath(ownerFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(p.Host)) || bytes.Contains(raw, p.Key) {
		t.Fatal("plaintext profile on disk")
	}
	if err = SetHost("device.local"); err != nil {
		t.Fatal(err)
	}
	q, err := Load()
	if err != nil || q.Host != "device.local" || !bytes.Equal(p.Key, q.Key) {
		t.Fatal("updated profile did not decrypt")
	}
	if err = os.WriteFile(file, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || errors.Is(err, ErrNotPaired) {
		t.Fatal("corrupt profile treated as unpaired")
	}
}

func TestLegacyCameraAddressFollowsItsKey(t *testing.T) {
	t.Setenv("ESP32AGENT_HOME", t.TempDir())
	first, second := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	for _, step := range []struct {
		key               []byte
		current, expected string
	}{
		{first, "192.0.2.10", "192.0.2.10"},
		{first, "", "192.0.2.10"},
		{second, "0.0.0.0", ""},
		{second, "192.0.2.20", "192.0.2.20"},
		{first, "", "192.0.2.10"},
	} {
		if err := saveLegacyPhoto(step.key, step.current); err != nil {
			t.Fatal(err)
		}
		key, host, err := LegacyPhoto()
		if err != nil || !bytes.Equal(key, step.key) || host != step.expected {
			t.Fatalf("incorrect camera selection, host=%q, err=%v", host, err)
		}
		oldKey, err := readProtected(photoFile)
		if err != nil || !bytes.Equal(oldKey, step.key) {
			t.Fatal("standalone client's 32-byte key changed format")
		}
	}
}
