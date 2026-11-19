//go:build windows || darwin || linux

package sensephoto

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/proto"
)

func testImage(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 13, 11)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func appendFrame(buf *bytes.Buffer, data []byte) {
	buf.Write(binary.LittleEndian.AppendUint16(nil, uint16(len(data))))
	buf.Write(data)
}

func servePhoto(conn net.Conn, key, data []byte) error {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil || line != "chal\n" {
		return errors.New("client did not request challenge")
	}
	boardNonce := bytes.Repeat([]byte{0xab}, 16)
	if _, err := fmt.Fprintf(conn, "chal %x\n", boardNonce); err != nil {
		return err
	}
	line, err = r.ReadString('\n')
	fields := strings.Fields(line)
	if err != nil || len(fields) != 3 || fields[0] != "photo" {
		return errors.New("client did not request photo")
	}
	clientNonce, err := hex.DecodeString(fields[1])
	if err != nil || len(clientNonce) != 16 {
		return errors.New("client nonce invalid")
	}
	proof, err := hex.DecodeString(fields[2])
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("photo"))
	mac.Write(clientNonce)
	mac.Write(boardNonce)
	if err != nil || !hmac.Equal(proof, mac.Sum(nil)[:16]) {
		return errors.New("client proof invalid")
	}
	_, b2c := proto.SessionKeys(key, clientNonce, boardNonce)
	aead, err := proto.NewAEAD(b2c)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("ok photo\n")
	appendFrame(&buf, aead.Seal('P', 'J', 1, data[:20]))
	appendFrame(&buf, aead.Seal('P', 'J', 2, data[20:]))
	appendFrame(&buf, aead.Seal('P', 'E', 3, nil))
	_, err = conn.Write(buf.Bytes())
	return err
}

func TestCaptureMockBoard(t *testing.T) {
	wanted := testImage(t)
	key := bytes.Repeat([]byte{0x11}, 32)
	client, server := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	server.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() { done <- servePhoto(server, key, wanted) }()
	got, err := capture(client, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got.width != 13 || got.height != 11 || !bytes.Equal(got.data, wanted) {
		t.Fatal("captured JPEG differs")
	}
}

func TestMalformedRecords(t *testing.T) {
	jpg := testImage(t)
	aead, err := proto.NewAEAD(make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	first := aead.Seal('P', 'J', 1, jpg)
	badTag := bytes.Clone(first)
	badTag[len(badTag)-1] ^= 1
	for _, test := range []struct {
		name   string
		frames [][]byte
		tail   []byte
	}{
		{"missing end", [][]byte{first}, nil},
		{"partial size", [][]byte{first}, []byte{17}},
		{"partial record", nil, []byte{18, 0, 'J', 1}},
		{"bad tag", [][]byte{badTag}, nil},
		{"wrong stream", [][]byte{aead.Seal('V', 'J', 1, jpg)}, nil},
		{"counter gap", [][]byte{first, aead.Seal('P', 'E', 3, nil)}, nil},
		{"bad end", [][]byte{first, aead.Seal('P', 'E', 2, []byte{1})}, nil},
		{"incomplete JPEG", [][]byte{aead.Seal('P', 'J', 1, jpg[:20]), aead.Seal('P', 'E', 2, nil)}, nil},
		{"invalid JPEG", [][]byte{aead.Seal('P', 'J', 1, []byte{0xff, 0xd8, 0, 0xff, 0xd9}), aead.Seal('P', 'E', 2, nil)}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stream bytes.Buffer
			for _, f := range test.frames {
				appendFrame(&stream, f)
			}
			stream.Write(test.tail)
			if got, err := readPhoto(&stream, aead); err == nil || got.data != nil {
				t.Fatal("malformed photo accepted")
			}
		})
	}
}

func TestSaveDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "photo.jpg")
	if err := saveJPEG(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := saveJPEG(path, []byte("replacement")); err == nil {
		t.Fatal("replaced existing file")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Fatal("original changed", err)
	}
}
