package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/upgrade"
)

// executable is the binary to replace (tests point it at a temp file).
var executable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Use the installed binary's own model pin. Older releases selected through
// custom indices can still be installed without a companion engine.
var setupReleaseOCR = func(exe string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	help, err := exec.CommandContext(ctx, exe, "--help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("starting installed CLI: %w", err)
	}
	if !bytes.Contains(help, []byte("  ocr install ")) {
		return nil
	}
	if output, err := exec.CommandContext(ctx, exe, "ocr", "install").CombinedOutput(); err != nil {
		return fmt.Errorf("%w (%s)", err, bytes.TrimSpace(output))
	}
	return nil
}

func (a *App) upgrade(args []string) int {
	fs := newFS(new(string))
	check := fs.Bool("check", false, "only report whether a newer version exists")
	force := fs.Bool("force", false, "install the latest even if it is not newer (or this is a dev build)")
	if _, err := parse(fs, args); err != nil {
		return a.fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	idx, err := upgrade.Fetch(ctx)
	if err != nil {
		return a.fail(fmt.Errorf("could not read the version list: %w", err))
	}
	dev := !upgrade.Valid(Version)
	newer := !dev && upgrade.Compare(idx.Latest, Version) > 0
	rel, ok := idx.Find(idx.Latest)
	if !ok {
		return a.fail(fmt.Errorf("version %s is not in the version list", idx.Latest))
	}
	asset, ok := rel.Assets[upgrade.Platform()]
	if !ok || asset.URL == "" {
		return a.fail(fmt.Errorf("version %s has no build for %s", idx.Latest, upgrade.Platform()))
	}
	if *check {
		out := map[string]any{"current": Version, "latest": idx.Latest, "update_available": newer}
		if dev {
			out["note"] = "development build: esp32agent upgrade --force installs " + idx.Latest
		}
		return a.print(daemon.Response{Status: "ok", Data: out})
	}
	if !newer && !*force {
		if dev {
			return a.fail(fmt.Errorf("this is a development build (%s); esp32agent upgrade --force installs %s", Version, idx.Latest))
		}
		// Older upgraders only replaced the CLI. Re-running upgrade completes a
		// previous installation even when the executable is already current.
		exe, err := executable()
		if err != nil {
			return a.fail(err)
		}
		if err := setupReleaseOCR(exe); err != nil {
			return a.fail(fmt.Errorf("CLI is current; retry esp32agent ocr install: %w", err))
		}
		return a.print(daemon.Response{Status: "ok", Data: map[string]any{"current": Version, "latest": idx.Latest,
			"message": "already up to date"}})
	}
	exe, err := executable()
	if err != nil {
		return a.fail(err)
	}
	if err := upgrade.Install(ctx, asset, exe); err != nil {
		if errors.Is(err, os.ErrPermission) {
			err = fmt.Errorf("%w (install the new version with the same rights as the old one)", err)
		}
		return a.fail(err)
	}
	stopped := a.Client != nil && a.Client.StopRunning() // the next command starts the new daemon
	if err := setupReleaseOCR(exe); err != nil {
		return a.fail(fmt.Errorf("upgraded CLI to %s; retry esp32agent ocr install: %w", idx.Latest, err))
	}
	return a.print(daemon.Response{Status: "ok", Data: map[string]any{"previous": Version, "version": idx.Latest,
		"path": exe, "daemon_stopped": stopped}})
}
