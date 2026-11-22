//go:build darwin || linux

package portableocr

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRestoreLauncherMode(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "rapidocr")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err := os.Lstat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureLauncherMode(exe, st.Mode()); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0700 {
		t.Fatalf("got permissions %o, want 0700", got)
	}
	if err := exec.Command(exe).Run(); err != nil {
		t.Fatal(err)
	}
}
