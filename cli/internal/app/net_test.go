package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/daemon"
)

func TestInteractiveDirectJoin(t *testing.T) {
	// Keep the UNIX socket path below the macOS limit.
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "e32n")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	l, err := daemon.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan daemon.Request, 4)
	var server sync.WaitGroup
	t.Cleanup(func() {
		l.Close()
		server.Wait()
	})
	server.Go(func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			server.Go(func() {
				defer nc.Close()
				stop := context.AfterFunc(t.Context(), func() { nc.Close() })
				defer stop()
				nc.SetDeadline(time.Now().Add(5 * time.Second))
				var req daemon.Request
				if json.NewDecoder(nc).Decode(&req) != nil {
					return // the client's socket liveness check
				}
				select {
				case requests <- req:
				default:
					t.Error("too many network requests")
					return
				}
				response := daemon.Response{Status: "ok", Data: map[string]any{"via": "wifi"}}
				switch {
				case req.Text == "join chosen network\x00private password":
					response.Data = map[string]any{"accepted": true}
				case req.Text != "":
					response = daemon.Response{Status: "error", Error: "board rejected credentials"}
				}
				json.NewEncoder(nc).Encode(response)
			})
		}
	})
	// A probe that hasn't delivered EOF must not hold up the actual requests.
	probe, err := net.DialTimeout("unix", daemon.SocketPath(dir), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	for _, test := range []struct {
		password string
		code     int
		pipe     bool
	}{
		{"private password", 0, false},
		{"private password", 0, true},
		{"other password", 1, false},
	} {
		var out, prompts bytes.Buffer
		args, input := []string{"net", "join"}, "chosen network\n"+test.password
		if test.pipe {
			args, input = append(args, "chosen network", "--password-stdin"), test.password
		}
		a := &App{Out: &out, Err: &prompts, In: strings.NewReader(input + "\n"),
			Client: &daemon.Client{Dir: dir, NoSpawn: true}}
		if code := a.Run(args); code != test.code {
			t.Fatalf("exit code %d, output %s", code, out.String())
		}
		if len(requests) != 2 {
			t.Fatalf("got %d network requests, want 2", len(requests))
		}
		if first, join := <-requests, <-requests; first.Text != "" || join.Text != "join chosen network\x00"+test.password {
			t.Fatal("unexpected network request")
		}
		if strings.Contains(out.String()+prompts.String(), test.password) || strings.Contains(prompts.String(), "Bluetooth") {
			t.Fatal("incorrect or sensitive prompt output")
		}
		if test.code != 0 && !strings.Contains(out.String(), "board rejected credentials") {
			t.Fatal("join error was lost")
		}
	}
}
