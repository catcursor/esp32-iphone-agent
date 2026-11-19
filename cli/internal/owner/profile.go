// Package owner keeps the USB-provisioned credential for direct LAN sessions.
package owner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

var ErrNotPaired = errors.New("no USB owner profile: run esp32agent pair-usb PORT")

const ownerFile = "owner.dpapi"
const photoFile = "sense-photo.key.dpapi"

type Profile struct {
	ClientID []byte
	BoardID  []byte
	Key      []byte
	Host     string
}

func (p *Profile) PhotoKey() []byte {
	h := hmac.New(sha256.New, p.Key)
	h.Write([]byte("sense-camera-v1"))
	return h.Sum(nil)
}

func ValidHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Is4() && !addr.IsUnspecified() && !addr.IsMulticast() && host != "255.255.255.255"
	}
	if len(host) == 0 || len(host) > 253 || strings.Trim(host, "0123456789.") == "" {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (p *Profile) valid() bool {
	return len(p.ClientID) == 16 && len(p.BoardID) == 6 && len(p.Key) == 32 && (p.Host == "" || ValidHost(p.Host))
}

func Load() (*Profile, error) {
	raw, err := readProtected(ownerFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotPaired
	}
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var p Profile
	if json.Unmarshal(raw, &p) != nil || !p.valid() {
		return nil, errors.New("invalid USB owner profile; pair again over USB")
	}
	return &p, nil
}

func save(p *Profile) error {
	if !p.valid() {
		return errors.New("invalid owner profile")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	defer clear(raw)
	return writeProtected(ownerFile, raw)
}

func SetHost(host string) error {
	if !ValidHost(host) {
		return errors.New("expected a board IPv4 address or hostname")
	}
	p, err := Load()
	if err != nil {
		return err
	}
	p.Host = host
	return save(p)
}

func parsePair(reply []byte) (*Profile, error) {
	var r struct{ ID, Board, Key, IP string }
	if json.Unmarshal(reply, &r) != nil {
		return nil, errors.New("board did not return an owner profile; check firmware and USB port")
	}
	id, e1 := hex.DecodeString(r.ID)
	board, e2 := hex.DecodeString(r.Board)
	key, e3 := hex.DecodeString(r.Key)
	if r.IP == "0.0.0.0" {
		r.IP = ""
	}
	p := &Profile{ClientID: id, BoardID: board, Key: key, Host: r.IP}
	if e1 != nil || e2 != nil || e3 != nil || !p.valid() {
		return nil, errors.New("board returned an invalid owner profile")
	}
	return p, nil
}

func Pair(port string) (*Profile, error) {
	reply, err := usbCommand(port, []byte("owner-pair"))
	if err != nil {
		return nil, err
	}
	defer clear(reply)
	p, err := parsePair(reply)
	if err != nil {
		return nil, err
	}
	if err = save(p); err != nil {
		return nil, err
	}
	return p, nil
}

func PairLegacyPhoto(port string) error {
	reply, err := usbCommand(port, []byte("photo-key"))
	if err != nil {
		return err
	}
	defer clear(reply)
	key := make([]byte, 32)
	defer clear(key)
	if len(reply) != 64 {
		return errors.New("board did not return a photo-only pairing key")
	}
	if _, err := hex.Decode(key, reply); err != nil {
		return errors.New("invalid photo-only pairing reply")
	}
	// Older camera firmware also reports the board's IP in ordinary status.
	// This command carries telemetry and does not request Wi-Fi credentials.
	var status struct{ IP string }
	raw, err := usbCommand(port, []byte("status"))
	if err != nil || json.Unmarshal(raw, &status) != nil {
		status.IP = ""
	}
	return saveLegacyPhoto(key, status.IP)
}

func photoAddressFile(key []byte) string {
	digest := sha256.Sum256(key)
	return "sense-photo." + hex.EncodeToString(digest[:]) + ".host.dpapi"
}

func saveLegacyPhoto(key []byte, host string) error {
	// Earlier clients read exactly 32 protected bytes from photoFile. Keep that
	// format. Write the address under the camera's fingerprint first, so replacing
	// the active key commits the pairing and can never select another camera's IP.
	if len(key) != 32 {
		return errors.New("invalid photo-only pairing key")
	}
	if ValidHost(host) {
		if err := writeProtected(photoAddressFile(key), []byte(host)); err != nil {
			return err
		}
	}
	return writeProtected(photoFile, key)
}

func LegacyPhoto() (key []byte, host string, err error) {
	key, err = readProtected(photoFile)
	if err != nil {
		return nil, "", fmt.Errorf("read photo-only credential: %w", err)
	}
	if len(key) != 32 {
		return nil, "", errors.New("invalid photo-only credential; pair again over USB")
	}
	raw, err := readProtected(photoAddressFile(key))
	if errors.Is(err, os.ErrNotExist) {
		return key, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	host = string(raw)
	if !ValidHost(host) {
		return nil, "", errors.New("invalid saved photo address; pair again over USB")
	}
	return key, host, nil
}
