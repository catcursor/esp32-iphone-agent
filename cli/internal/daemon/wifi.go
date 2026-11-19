package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/owner"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/proto"
)

// wifiLink moves a board's session to its Wi-Fi link when this computer can reach the board's IP (same Wi-Fi,
// or joined to the board's hotspot), and back to BLE when that link drops. BLE stays connected the whole time:
// it finds the board, carries the IP (status), and is the way back. docs/cli-protocol.md, "Wi-Fi link".
type wifiLink struct {
	dial  func(ctx context.Context, ip string) (proto.Link, error)
	hello func(ctx context.Context, l proto.Link) (*proto.Session, error)
	every time.Duration // how often run checks

	mu      sync.Mutex
	mode    string    // "auto": Wi-Fi when reachable; "ble": stay on BLE
	retryAt time.Time // after a failed try, or a fall back
	backoff time.Duration
	lastErr string
	done    chan struct{}
	once    sync.Once
	moving  bool
}

const (
	wifiCheck      = 2 * time.Second
	wifiBackoffMin = 10 * time.Second
	wifiBackoffMax = time.Minute
)

// TransportMode is ESP32AGENT_TRANSPORT: "ble" keeps sessions on BLE; anything else is "auto".
func TransportMode() string {
	if os.Getenv("ESP32AGENT_TRANSPORT") == "ble" {
		return "ble"
	}
	return "auto"
}

func newWifiLink(dial func(context.Context, string) (proto.Link, error),
	hello func(context.Context, proto.Link) (*proto.Session, error)) *wifiLink {
	return &wifiLink{dial: dial, hello: hello, every: wifiCheck, mode: TransportMode(), done: make(chan struct{})}
}

func (w *wifiLink) stop() { w.once.Do(func() { close(w.done) }) }

func (w *wifiLink) setMode(m string) {
	w.mu.Lock()
	w.mode, w.retryAt, w.backoff, w.lastErr = m, time.Time{}, 0, ""
	w.mu.Unlock()
}

func (w *wifiLink) state() (mode, lastErr string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mode, w.lastErr
}

// claim marks a move in progress; false if one already is.
func (w *wifiLink) claim() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.moving {
		return false
	}
	w.moving = true
	return true
}

func (w *wifiLink) release() {
	w.mu.Lock()
	w.moving = false
	w.mu.Unlock()
}

func (w *wifiLink) failed(err string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.backoff = min(max(w.backoff*2, wifiBackoffMin), wifiBackoffMax)
	w.retryAt = time.Now().Add(w.backoff)
	w.lastErr = err
}

// run checks every wifiCheck whether the session should move, until stop.
func (w *wifiLink) run(c *Conn) {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-t.C:
		}
		w.mu.Lock()
		mode, due := w.mode, time.Now().After(w.retryAt)
		w.mu.Unlock()
		switch via := c.Via(); {
		case via == "wifi" && mode == "ble":
			c.mu.Lock()
			bl := c.bleLink
			c.mu.Unlock()
			if bl != nil {
				w.fallBack(c, bl)
			}
		case via == "ble" && mode == "auto" && due:
			if ip := boardIP(c); ip != "" {
				w.up(c, ip)
			}
		}
	}
}

// boardIP is the board's address from its status, "" while it has none (station mode, not connected).
func boardIP(c *Conn) string {
	st, _ := c.Status()
	ip, _ := st["ip"].(string)
	if n, _ := st["net"].(string); n != "ap" && n != "sta" {
		return ""
	}
	if a := net.ParseIP(ip); a == nil || a.IsUnspecified() {
		return ""
	}
	return ip
}

// up opens the Wi-Fi link and moves the session to it.
func (w *wifiLink) up(c *Conn, ip string) {
	if !w.claim() {
		return
	}
	defer w.release()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	dctx, dcancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	l, err := w.dial(dctx, ip)
	dcancel()
	if err != nil {
		w.failed("board's Wi-Fi not reachable from this computer (" + ip + ")")
		return
	}
	sess, err := w.hello(ctx, l)
	if err != nil || idHex(sess.BoardID) != c.ID {
		l.Close()
		w.failed("Wi-Fi link refused: " + errText(err))
		return
	}
	if c.Via() != "ble" { // the BLE session ended meanwhile: the next request reconnects
		sess.Close()
		return
	}
	c.attach(sess, "wifi", l)
	if err := c.start(); err != nil {
		w.failed("Wi-Fi link: " + err.Error())
		c.detach(sess, err) // falls back to BLE
		return
	}
	w.mu.Lock()
	w.backoff, w.lastErr = 0, ""
	w.mu.Unlock()
	log.Printf("%s: session moved to Wi-Fi (%s)", c.ID, ip)
}

// fallBack opens a new session on the BLE link bl, kept while the session ran over Wi-Fi.
func (w *wifiLink) fallBack(c *Conn, bl proto.Link) {
	w.failed("Wi-Fi link dropped")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sess, err := w.hello(ctx, bl)
	if err == nil {
		if err = c.attach(sess, "ble", bl); err == nil {
			err = c.start()
		}
		if err == nil {
			log.Printf("%s: session back on BLE", c.ID)
			return
		}
		c.detach(sess, err)
	}
	log.Printf("%s: no way back over BLE (%v); the next request reconnects", c.ID, err)
	c.mu.Lock()
	if c.bleLink == bl {
		c.bleLink = nil
	}
	c.mu.Unlock()
	bl.Close()
}

func errText(err error) string {
	if err == nil {
		return "another board"
	}
	return err.Error()
}

const addressHint = "If the address changed, run esp32agent target HOST with the new address or esp32agent pair-usb PORT to read it from the board."

// net reports or changes Wi-Fi settings over the current encrypted command session.
func (s *Server) net(ctx context.Context, c *Conn, arg string) Response {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	verb, rest, _ := strings.Cut(arg, " ")
	switch verb {
	case "":
	case "hotspot-password":
		if rest != "" {
			return fail(errors.New("usage: net hotspot-password"))
		}
		// Older boards put the AP password in the authenticated net status, and
		// reject the separate command with a usage error. Preserve explicit retrieval.
		reply, err := c.Send(ctx, "net hotspot-password")
		if err != nil && strings.HasPrefix(err.Error(), "usage: net [") {
			reply, err = c.Send(ctx, "net")
		}
		if err != nil {
			return fail(err)
		}
		var ap struct {
			Mode     string `json:"mode"`
			SSID     string `json:"ssid"`
			Password string `json:"pass"`
		}
		if json.Unmarshal([]byte(reply), &ap) != nil || ap.Mode != "ap" {
			return fail(errors.New("the board is not in hotspot mode"))
		}
		if ap.Password == "" {
			return fail(errors.New("board did not return its hotspot password"))
		}
		return ok(map[string]string{"ssid": ap.SSID, "password": ap.Password})
	case "ipv4", "static", "dhcp":
		cmd := "ipv4"
		switch verb {
		case "static":
			ip, err := netip.ParseAddr(rest)
			if err != nil || !ip.Is4() || ip.IsLoopback() || ip.As4()[0] == 0 || ip.As4()[0] >= 224 {
				return fail(errors.New("usage: net static <unicast IPv4 address>"))
			}
			cmd += " static " + ip.String()
		case "dhcp":
			cmd += " dhcp"
		}
		if verb != "static" && rest != "" {
			return fail(errors.New("usage: net ipv4 | net static IPv4 | net dhcp"))
		}
		reply, err := c.Send(ctx, cmd)
		if err != nil {
			return fail(err)
		}
		var policy struct {
			Policy  string `json:"policy"`
			Address string `json:"address"`
		}
		if json.Unmarshal([]byte(reply), &policy) != nil || policy.Policy == "" {
			return fail(errors.New("board did not return an IPv4 policy"))
		}
		out := map[string]any{"via": c.Via(), "ipv4": policy}
		if verb != "ipv4" && c.wifi == nil {
			out["message"] = addressHint
		}
		return ok(out)
	case "via":
		if rest != "auto" && rest != "ble" {
			return fail(errors.New("usage: net via auto|ble"))
		}
		if c.wifi == nil {
			if rest == "ble" {
				return fail(errors.New("USB-paired sessions use Wi-Fi; ESP32AGENT_TRANSPORT=ble selects legacy Bluetooth pairing on daemon startup"))
			}
		} else {
			c.wifi.setMode(rest)
		}
	case "join", "hotspot":
		var settings []byte
		if verb == "hotspot" {
			if rest != "" {
				return fail(errors.New("usage: net hotspot"))
			}
			settings = []byte(`{"mode":"ap"}`)
		} else {
			ssid, pass, _ := strings.Cut(rest, "\x00")
			if ssid == "" {
				return fail(errors.New("usage: net join SSID [PASSWORD]"))
			}
			var err error
			settings, err = owner.WiFiJoinJSON(ssid, pass)
			if err != nil {
				return fail(err)
			}
		}
		if _, err := c.Send(ctx, "net "+string(settings)); err != nil {
			return fail(err)
		}
		// Changing the station or switching to the hotspot can drop this very TCP link.
		if c.wifi == nil {
			return ok(map[string]any{"accepted": true, "via": "wifi", "message": addressHint})
		}
		c.wifi.setMode(TransportMode()) // try the new network soon
	default:
		return fail(errors.New("usage: net [via auto|ble | join | hotspot | hotspot-password | ipv4 | static IPv4 | dhcp]"))
	}
	reply, err := c.Send(ctx, "net")
	if err != nil {
		return fail(err)
	}
	board, err := owner.ParseNetwork([]byte(reply))
	if err != nil {
		return fail(err)
	}
	mode, lastErr := "wifi", ""
	if c.wifi != nil {
		mode, lastErr = c.wifi.state()
	}
	out := map[string]any{"via": c.Via(), "prefer": mode, "board": board}
	if lastErr != "" && c.Via() != "wifi" {
		out["wifi_error"] = lastErr
	}
	return ok(out)
}
