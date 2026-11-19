package owner

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPairValidatesFirmwareIdentity(t *testing.T) {
	id, board, key := strings.Repeat("10", 16), strings.Repeat("20", 6), strings.Repeat("30", 32)
	for _, test := range []struct {
		name, id, board, key, ip string
		ok                       bool
	}{
		{"valid", id, board, key, "192.0.2.14", true}, {"not on wifi", id, board, key, "0.0.0.0", true},
		{"bad id", id[2:], board, key, "192.0.2.14", false}, {"bad board", id, board + "11", key, "", false},
		{"bad key", id, board, "x" + key[1:], "", false}, {"bad target", id, board, key, "host:port", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]string{"id": test.id, "board": test.board, "key": test.key, "ip": test.ip})
			p, err := parsePair(payload)
			if (err == nil) != test.ok {
				t.Fatalf("unexpected pairing result, err=%v", err)
			}
			if test.ok && test.ip == "0.0.0.0" && p.Host != "" {
				t.Fatal("saved unspecified target")
			}
			if err != nil && strings.Contains(err.Error(), test.key) {
				t.Fatal("key disclosed")
			}
		})
	}
}

func TestCameraDerivation(t *testing.T) {
	p := &Profile{Key: bytes.Repeat([]byte{'k'}, 32)}
	h := hmac.New(sha256.New, p.Key)
	h.Write([]byte("sense-camera-v1"))
	if bytes.Equal(p.Key, p.PhotoKey()) || !bytes.Equal(h.Sum(nil), p.PhotoKey()) {
		t.Fatal("wrong subkey")
	}
}

func TestWiFiCredentialsPreserved(t *testing.T) {
	buf, err := WiFiJoinJSON("屋<&>", "pa&ss\"\\<>")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf, []byte(`\u`)) {
		t.Fatal("encoded unicode escapes unsupported by firmware")
	}
	var netcfg map[string]string
	if json.Unmarshal(buf, &netcfg) != nil || netcfg["ssid"] != "屋<&>" || netcfg["pass"] != "pa&ss\"\\<>" || netcfg["mode"] != "sta" {
		t.Fatal("configuration changed in transit")
	}
	for _, x := range []string{"abc\nssid", strings.Repeat("a", 33), "\u2028"} {
		if _, err := WiFiJoinJSON(x, "password"); err == nil {
			t.Fatal("accepted unrepresentable SSID")
		}
	}
}

func TestStatusDropsCredentials(t *testing.T) {
	raw := []byte(`{"mode":"sta","ssid":"example","ip":"192.0.2.10","pass":"test-secret","vtok":"test-token","extra":"unknown"}`)
	v, err := ParseNetwork(raw)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.Marshal(v)
	if bytes.Contains(j, []byte("test-secret")) || bytes.Contains(j, []byte("test-token")) || bytes.Contains(j, []byte("unknown")) {
		t.Fatal("network status leaked credentials")
	}
	if _, err := ParseNetwork([]byte("err missing")); err == nil {
		t.Fatal("invalid reply accepted")
	}
}

func TestUSBHotspotPasswordOnlyInAPMode(t *testing.T) {
	if result, err := parseHotspotPassword([]byte("err hotspot mode required")); err == nil || result != nil || !strings.Contains(err.Error(), "hotspot mode") {
		t.Fatal("firmware's station-mode refusal did not reach the user")
	}
	for _, mode := range []string{"ap", "sta", ""} {
		body, _ := json.Marshal(map[string]string{"mode": mode, "ssid": "test-ap", "pass": "sample-pass", "vtok": "private-token"})
		reply, err := parseHotspotPassword(body)
		if mode == "ap" {
			if err != nil || len(reply) != 2 || reply["password"] != "sample-pass" || reply["ssid"] != "test-ap" {
				t.Fatal("AP response should contain only SSID and password")
			}
		} else if err == nil || reply != nil || strings.Contains(err.Error(), "sample-pass") {
			t.Fatal("station credentials must stay private")
		}
	}
}

func TestHosts(t *testing.T) {
	for _, s := range []string{"host", "board.local.", "192.0.2.14"} {
		if !ValidHost(s) {
			t.Error(s)
		}
	}
	for _, s := range []string{"", "host/path", "no..host", "-name", "0.0.0.0", "999.0.0.1", "224.0.0.1", "bad:7202"} {
		if ValidHost(s) {
			t.Error(s)
		}
	}
	if _, err := parsePair(nil); errors.Is(err, ErrNotPaired) {
		t.Fatal("malformed board reply treated as missing profile")
	}
}
