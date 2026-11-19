package owner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var comPort = regexp.MustCompile(`^COM[1-9][0-9]{0,3}$`)

const ownerFile = "owner.dpapi"
const credentialSuffix = ".dpapi"

func usbCommand(port string, command []byte) ([]byte, error) {
	port = strings.ToUpper(strings.TrimSpace(port))
	if !comPort.MatchString(port) {
		return nil, errors.New("specify a Windows serial port such as COM6")
	}
	if len(command) > 509 || bytes.ContainsAny(command, "\r\n") {
		return nil, errors.New("invalid USB request")
	}
	name, err := windows.UTF16PtrFromString(`\\.\` + port)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", port, err)
	}
	defer windows.CloseHandle(h)
	var state windows.DCB
	state.DCBlength = uint32(unsafe.Sizeof(state))
	if err = windows.GetCommState(h, &state); err != nil {
		return nil, err
	}
	state.BaudRate = windows.CBR_115200
	state.ByteSize = 8
	state.Parity = windows.NOPARITY
	state.StopBits = windows.ONESTOPBIT
	state.Flags = 1 | windows.DTR_CONTROL_ENABLE | windows.RTS_CONTROL_ENABLE
	if err = windows.SetCommState(h, &state); err != nil {
		return nil, err
	}
	timeouts := windows.CommTimeouts{ReadIntervalTimeout: 25, ReadTotalTimeoutConstant: 250, WriteTotalTimeoutConstant: 2000}
	if err = windows.SetCommTimeouts(h, &timeouts); err != nil {
		return nil, err
	}
	if err = windows.PurgeComm(h, windows.PURGE_RXCLEAR); err != nil {
		return nil, err
	}
	request := append([]byte("0 "), command...)
	request = append(request, '\n')
	defer clear(request)
	var written uint32
	if err = windows.WriteFile(h, request, &written, nil); err != nil {
		return nil, err
	}
	if int(written) != len(request) {
		return nil, io.ErrShortWrite
	}
	var buf [256]byte
	defer clear(buf[:])
	var line [2048]byte
	defer clear(line[:])
	length := 0
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		var n uint32
		if err = windows.ReadFile(h, buf[:], &n, nil); err != nil {
			return nil, err
		}
		for _, b := range buf[:n] {
			if b != '\n' {
				if length < len(line) {
					line[length] = b
					length++
				}
				continue
			}
			reply := bytes.TrimSuffix(line[:length], []byte{'\r'})
			length = 0
			if bytes.HasPrefix(reply, []byte("0 ")) {
				return bytes.Clone(reply[2:]), nil
			}
		}
	}
	return nil, errors.New("no numbered reply from the selected USB port")
}

func protectedPath(name string) (string, error) {
	if root := os.Getenv("ESP32AGENT_HOME"); root != "" {
		return filepath.Join(root, name), nil
	}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "ESP32Agent", name), nil
}

func writeProtected(name string, plain []byte) error {
	path, err := protectedPath(name)
	if err != nil {
		return err
	}
	if len(plain) == 0 {
		return errors.New("empty credential")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".owner-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(unsafe.Slice(out.Data, out.Size)); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp.Name(), path)
}

func readProtected(name string) ([]byte, error) {
	path, err := protectedPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > 16*1024 {
		return nil, errors.New("invalid encrypted credential file")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("unlock USB credential: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	raw := unsafe.Slice(out.Data, out.Size)
	defer clear(raw)
	return bytes.Clone(raw), nil
}
