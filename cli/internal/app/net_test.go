package app

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"

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
	defer os.RemoveAll(dir)
	l, err := daemon.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	requests := make(chan daemon.Request, 4)
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			func(c net.Conn) {
				defer c.Close()
				var req daemon.Request
				if json.NewDecoder(c).Decode(&req) != nil {
					return // the client's socket liveness check
				}
				requests <- req
				response := daemon.Response{Status: "ok", Data: map[string]any{"via": "wifi"}}
				switch {
				case req.Text == "join chosen network\x00private password":
					response.Data = map[string]any{"accepted": true}
				case req.Text != "":
					response = daemon.Response{Status: "error", Error: "board rejected credentials"}
				}
				json.NewEncoder(c).Encode(response)
			}(nc)
		}
	}()
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
