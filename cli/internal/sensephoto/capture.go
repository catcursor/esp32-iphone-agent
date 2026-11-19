package sensephoto

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"strings"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/proto"
)

type photo struct {
	data          []byte
	width, height int
}

func boardLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		return "", fmt.Errorf("reading board reply: %w", err)
	}
	line = bytes.TrimSuffix(line[:len(line)-1], []byte{'\r'})
	if bytes.HasPrefix(line, []byte("err ")) {
		return "", fmt.Errorf("board error: %q", line[4:])
	}
	return string(line), nil
}

func capture(conn io.ReadWriter, key []byte) (photo, error) {
	if len(key) != 32 {
		return photo{}, errors.New("invalid pairing key")
	}
	if _, err := io.WriteString(conn, "chal\n"); err != nil {
		return photo{}, err
	}
	r := bufio.NewReaderSize(conn, 256)
	line, err := boardLine(r)
	if err != nil {
		return photo{}, err
	}
	encoded, ok := strings.CutPrefix(line, "chal ")
	if !ok || len(encoded) != 32 {
		return photo{}, errors.New("invalid board challenge")
	}
	boardNonce, err := hex.DecodeString(encoded)
	if err != nil {
		return photo{}, errors.New("invalid board nonce")
	}
	clientNonce := make([]byte, 16)
	if _, err := rand.Read(clientNonce); err != nil {
		return photo{}, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("photo"))
	mac.Write(clientNonce)
	mac.Write(boardNonce)
	if _, err := fmt.Fprintf(conn, "photo %x %x\n", clientNonce, mac.Sum(nil)[:16]); err != nil {
		return photo{}, err
	}
	line, err = boardLine(r)
	if err != nil {
		return photo{}, err
	}
	if line != "ok photo" {
		return photo{}, errors.New("unexpected photo response")
	}
	_, b2c := proto.SessionKeys(key, clientNonce, boardNonce)
	aead, err := proto.NewAEAD(b2c)
	if err != nil {
		return photo{}, err
	}
	return readPhoto(r, aead)
}

func readPhoto(r io.Reader, aead *proto.AEAD) (photo, error) {
	const overhead = 5 + 12 // type, counter, GCM tag
	const maxJPEG = 4 << 20
	var frame [overhead + 1024]byte
	var encoded bytes.Buffer
	for next := uint32(1); ; next++ {
		var length [2]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return photo{}, fmt.Errorf("missing photo record: %w", err)
		}
		n := int(binary.LittleEndian.Uint16(length[:]))
		if n < overhead || n > len(frame) {
			return photo{}, errors.New("invalid photo record length")
		}
		if _, err := io.ReadFull(r, frame[:n]); err != nil {
			return photo{}, fmt.Errorf("incomplete photo record: %w", err)
		}
		counter, data, err := aead.Open('P', frame[:n])
		if err != nil {
			return photo{}, fmt.Errorf("invalid photo record: %w", err)
		}
		if counter != next {
			return photo{}, errors.New("photo record out of sequence")
		}
		switch frame[0] {
		case 'J':
			if len(data) == 0 || encoded.Len()+len(data) > maxJPEG {
				return photo{}, errors.New("invalid photo payload size")
			}
			encoded.Write(data)
		case 'E':
			if len(data) != 0 {
				return photo{}, errors.New("invalid end-of-photo record")
			}
			return validateJPEG(encoded.Bytes())
		default:
			return photo{}, errors.New("unknown photo record")
		}
	}
}

func validateJPEG(data []byte) (photo, error) {
	if !bytes.HasPrefix(data, []byte{0xff, 0xd8}) || !bytes.HasSuffix(data, []byte{0xff, 0xd9}) {
		return photo{}, errors.New("missing JPEG start or end")
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return photo{}, fmt.Errorf("invalid JPEG header: %w", err)
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 6_000_000 {
		return photo{}, errors.New("invalid JPEG dimensions")
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		return photo{}, fmt.Errorf("invalid JPEG: %w", err)
	}
	return photo{data: data, width: config.Width, height: config.Height}, nil
}
