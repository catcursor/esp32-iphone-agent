package owner

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Network struct {
	Mode string         `json:"mode"`
	SSID string         `json:"ssid"`
	IP   string         `json:"ip"`
	IPv4 *addressPolicy `json:"ipv4,omitempty"`
}

type addressPolicy struct {
	Policy  string `json:"policy"`
	Address string `json:"address"`
}

func ParseNetwork(data []byte) (Network, error) {
	var status Network
	if err := json.Unmarshal(data, &status); err != nil || status.Mode == "" {
		return Network{}, errors.New("board returned an invalid Wi-Fi status")
	}
	return status, nil
}

func WiFiStatus(port string) (Network, error) {
	raw, err := usbCommand(port, []byte("netstatus"))
	if err != nil {
		return Network{}, err
	}
	defer clear(raw)
	n, err := ParseNetwork(raw)
	if err != nil {
		return Network{}, err
	}
	policy, err := usbCommand(port, []byte("ipv4"))
	if err == nil {
		var v addressPolicy
		if json.Unmarshal(bytes.TrimPrefix(policy, []byte("ok ")), &v) == nil && v.Policy != "" {
			n.IPv4 = &v
		}
	}
	return n, nil
}

// WiFiHotspotPassword retrieves the board-generated password over physical USB.
func WiFiHotspotPassword(port string) (map[string]string, error) {
	raw, err := usbCommand(port, []byte("hotspot-password"))
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	return parseHotspotPassword(raw)
}

func parseHotspotPassword(raw []byte) (map[string]string, error) {
	if bytes.Equal(raw, []byte("err hotspot mode required")) {
		return nil, errors.New("the board must be in hotspot mode")
	}
	var reply struct{ Mode, SSID, Pass string }
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, errors.New("board returned an invalid hotspot reply")
	}
	if reply.Mode != "ap" {
		return nil, errors.New("the board must be in hotspot mode")
	}
	if reply.SSID == "" || reply.Pass == "" {
		return nil, errors.New("board did not report its hotspot credentials")
	}
	return map[string]string{"ssid": reply.SSID, "password": reply.Pass}, nil
}

func WiFiJoinJSON(ssid, pass string) ([]byte, error) {
	if len(ssid) == 0 || len(ssid) > 32 || !utf8.ValidString(ssid) || !utf8.ValidString(pass) ||
		len(pass) > 64 || strings.IndexFunc(ssid+pass, unicode.IsControl) >= 0 || strings.ContainsAny(ssid+pass, "\u2028\u2029") {
		return nil, errors.New("invalid Wi-Fi name or password")
	}
	// Firmware handles literal UTF-8 and quote/backslash escapes in its flat JSON command.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string{"mode": "sta", "ssid": ssid, "pass": pass}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func applyWiFi(port string, command []byte) error {
	raw, err := usbCommand(port, command)
	if err != nil {
		return err
	}
	if string(raw) == "ok" || bytes.HasPrefix(raw, []byte("ok ")) {
		return nil
	}
	return errors.New("board rejected the Wi-Fi request; check mode and firmware support")
}

func WiFiJoin(port, ssid, pass string) error {
	body, err := WiFiJoinJSON(ssid, pass)
	if err != nil {
		return err
	}
	defer clear(body)
	cmd := append([]byte("net "), body...)
	defer clear(cmd)
	return applyWiFi(port, cmd)
}

func WiFiHotspot(port string) error { return applyWiFi(port, []byte(`net {"mode":"ap"}`)) }
func WiFiStatic(port, address string) error {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ValidHost(address) {
		return errors.New("invalid static IPv4 address")
	}
	return applyWiFi(port, []byte("ipv4 static "+ip.String()))
}
func WiFiDHCP(port string) error { return applyWiFi(port, []byte("ipv4 dhcp")) }
