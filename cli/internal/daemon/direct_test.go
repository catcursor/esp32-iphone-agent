package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/config"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/owner"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/proto"
)

// Provision a key out of band, as USB does on the physical board. A nil Bluetooth
// transport makes any attempt to enable Bluetooth or discover boards fail the test.
func directBoard(t *testing.T) (*Server, *proto.FakeBoard) {
	t.Helper()
	b := fakeBoard()
	client := make([]byte, 16)
	link := b.Connect()
	key, id, err := proto.Pair(t.Context(), link, client)
	link.Close()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Owner: &owner.Profile{ClientID: client, Key: key, BoardID: id, Host: "board.test"},
		Config: &config.Config{}, Dir: t.TempDir(), conns: map[string]*Conn{}}
	s.DialWiFi = func(_ context.Context, host string) (proto.Link, error) {
		if host != s.Owner.Host {
			t.Errorf("unexpected dial target %q", host)
		}
		return b.Connect(), nil
	}
	t.Cleanup(s.closeAll)
	return s, b
}

func TestDirectSessionReconnectAndSelection(t *testing.T) {
	s, b := directBoard(t)
	if r := s.Do(t.Context(), Request{Cmd: "devices"}); r.Status != "ok" || len(s.conns) != 0 {
		t.Fatalf("devices: %+v", r)
	}
	if r := s.Do(t.Context(), Request{Cmd: "info", Device: "ffff"}); r.Status != "error" || len(s.conns) != 0 {
		t.Fatalf("other board selected: %+v", r)
	}
	var wg sync.WaitGroup
	for _, id := range []string{"", "6AD5", idHex(b.ID)} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c, err := s.conn(t.Context(), id); err != nil || c.Via() != "wifi" {
				t.Errorf("connect: %v", err)
			}
		}()
	}
	wg.Wait()
	c := s.conns[idHex(b.ID)]
	b.Status(`{"fw":"test","up":1200}`)
	r := s.Do(t.Context(), Request{Cmd: "info"})
	if st, ok := r.Data.(map[string]any); !ok || st["fw"] != "test" || st["via"] != "wifi" {
		t.Fatalf("status: %+v", r)
	}
	b.SkipB()
	b.Status(`{}`)
	for deadline := time.Now().Add(time.Second); c.connected() && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if c.connected() {
		t.Fatal("session did not disconnect")
	}
	if r := s.Do(t.Context(), Request{Cmd: "keys", Text: "esc"}); r.Status != "ok" {
		t.Fatalf("key after reconnect: %+v", r)
	}
	if _, age := c.Status(); age >= 0 {
		t.Fatal("new session retained the disconnected board's status")
	}
	if got := strings.Join(b.Commands, ","); got != "stream on,stream on,key esc" {
		t.Fatalf("unexpected commands %q", got)
	}
}

func TestDirectSessionRequiresProvisionedBoardIdentity(t *testing.T) {
	s, b := directBoard(t)
	s.Owner.BoardID = mustHex("000102030405") // the board still has the right key
	if r := s.Do(t.Context(), Request{Cmd: "button", Text: "HOME"}); r.Status != "error" || len(b.Commands) != 0 {
		t.Fatalf("sent a command to a different board: %+v", r)
	}
	s.Owner.BoardID = b.ID
	s.Owner.Key[0] ^= 1
	if r := s.Do(t.Context(), Request{Cmd: "keys", Text: "esc"}); r.Status != "error" || len(b.Commands) != 0 {
		t.Fatalf("sent a command with the wrong key: %+v", r)
	}
	s.Owner.Host = ""
	s.DialWiFi = func(context.Context, string) (proto.Link, error) {
		t.Fatal("dialed with an empty target")
		return nil, nil
	}
	if r := s.Do(t.Context(), Request{Cmd: "info"}); r.Status != "error" || !strings.Contains(r.Error, "target") {
		t.Fatalf("missing address: %+v", r)
	}
}

func TestStopRunningReleasesSocketForNewProfile(t *testing.T) {
	client, server := start(t, fakeTransport{fakeBoard()})
	if !client.StopRunning() {
		t.Fatal("daemon did not stop")
	}
	l, err := Listen(server.Dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

func TestDirectNetworkCommands(t *testing.T) {
	s, b := directBoard(t)
	mode := "ap"
	legacy := false
	b.Handle = func(cmd string) string {
		switch {
		case cmd == "net hotspot-password" && legacy:
			return "err usage: net [{...}]"
		case cmd == "net hotspot-password" && mode != "ap":
			return "err board must be in hotspot mode"
		case cmd == "net hotspot-password":
			return `ok {"mode":"ap","ssid":"desk","pass":"private"}`
		case cmd == "net" && !legacy:
			return `ok {"mode":"` + mode + `","ssid":"desk","ip":"192.0.2.1"}`
		case cmd == "net":
			return `ok {"mode":"` + mode + `","ssid":"desk","ip":"192.0.2.1","pass":"private","vtok":"private"}`
		case strings.HasPrefix(cmd, "ipv4"):
			return `ok {"policy":"dhcp","address":""}`
		default:
			return "ok"
		}
	}
	for _, state := range []struct {
		mode   string
		legacy bool
	}{{"ap", false}, {"sta", false}, {"ap", true}, {"sta", true}} {
		mode, legacy = state.mode, state.legacy
		for _, req := range []Request{{Cmd: "net"}, {Cmd: "send", Text: "net"}} {
			before := len(b.Commands)
			r := s.Do(t.Context(), req)
			raw, _ := json.Marshal(r)
			if r.Status != "ok" || strings.Contains(string(raw), "private") || !strings.Contains(string(raw), "desk") {
				t.Fatalf("net credentials or missing public fields (%s)", mode)
			}
			if got := strings.Join(b.Commands[before:], ","); got != "net" && got != "stream on,net" {
				t.Fatalf("unexpected public status request %q", got)
			}
		}
		before := len(b.Commands)
		r := s.Do(t.Context(), Request{Cmd: "net", Text: "hotspot-password"})
		want := "net hotspot-password"
		if legacy {
			want += ",net"
		}
		if got := strings.Join(b.Commands[before:], ","); got != want {
			t.Fatalf("unexpected hotspot query %q", got)
		}
		if mode == "ap" {
			ap, ok := r.Data.(map[string]string)
			if r.Status != "ok" || !ok || ap["ssid"] != "desk" || ap["password"] != "private" {
				t.Fatal("hotspot password unavailable in hotspot mode")
			}
		} else if raw, _ := json.Marshal(r); r.Status != "error" || strings.Contains(string(raw), "private") {
			t.Fatal("hotspot-password exposed credentials while in station mode")
		}
	}
	for _, test := range []struct{ arg, wire string }{
		{"ipv4", "ipv4"}, {"static 192.0.2.6", "ipv4 static 192.0.2.6"}, {"dhcp", "ipv4 dhcp"},
		{"hotspot", `net {"mode":"ap"}`}, {"join lab name\x00trailing pass ", ""},
	} {
		before := len(b.Commands)
		r := s.Do(t.Context(), Request{Cmd: "net", Text: test.arg})
		if r.Status != "ok" || len(b.Commands) != before+1 {
			t.Fatalf("network command: %+v, sent %d commands", r, len(b.Commands)-before)
		}
		wire := b.Commands[before]
		if test.wire != "" && wire != test.wire {
			t.Fatalf("wrong board command")
		}
		if test.wire == "" {
			var config map[string]string
			raw, _ := json.Marshal(r)
			if json.Unmarshal([]byte(strings.TrimPrefix(wire, "net ")), &config) != nil || config["pass"] != "trailing pass " || config["ssid"] != "lab name" || strings.Contains(string(raw), "trailing pass") {
				t.Fatal("join did not preserve credentials privately")
			}
		}
	}
	before := len(b.Commands)
	for _, arg := range []string{"static 0.0.0.0", "static ::1", "static 224.0.0.1", "dhcp extra"} {
		if r := s.Do(t.Context(), Request{Cmd: "net", Text: arg}); r.Status != "error" || len(b.Commands) != before {
			t.Fatalf("invalid argument accepted: %s", arg)
		}
	}
}
