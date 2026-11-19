//go:build darwin || linux

package owner

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// A USB tty can implement poll while declining registration with Darwin's
// kqueue. Use the tty's poll interface directly for bounded reads and writes.
type serialPort struct {
	fd       int
	deadline time.Time
}

func (p serialPort) wait(events int16) error {
	fds := []unix.PollFd{{Fd: int32(p.fd), Events: events}}
	for {
		left := time.Until(p.deadline)
		if left <= 0 {
			return os.ErrDeadlineExceeded
		}
		ms := int((left + time.Millisecond - 1) / time.Millisecond)
		n, err := unix.Poll(fds, ms)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return os.ErrDeadlineExceeded
		}
		return nil // the ensuing read/write also reports hangup or a disconnected device
	}
}

func (p serialPort) Read(b []byte) (int, error) {
	for {
		if err := p.wait(unix.POLLIN); err != nil {
			return 0, err
		}
		n, err := unix.Read(p.fd, b)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
}

func (p serialPort) send(b []byte) error {
	for len(b) > 0 {
		if err := p.wait(unix.POLLOUT); err != nil {
			return err
		}
		n, err := unix.Write(p.fd, b)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func usbCommand(port string, command []byte) ([]byte, error) {
	if !filepath.IsAbs(port) {
		return nil, errors.New("specify the USB serial device path, for example /dev/ttyACM0 or /dev/cu.usbmodem…")
	}
	if len(command) > 509 || bytes.ContainsAny(command, "\r\n") {
		return nil, errors.New("invalid USB request")
	}
	fd, err := unix.Open(port, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", port, err)
	}
	defer unix.Close(fd)
	state, err := unix.IoctlGetTermios(fd, getTTY)
	if err != nil {
		return nil, fmt.Errorf("read serial settings on %s: %w", port, err)
	}
	// Exclusive access prevents overlapping exchanges from consuming each other's
	// numbered replies. Flush any old input as the port switches to 115200 8N1.
	if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
		return nil, fmt.Errorf("claim serial port %s: %w", port, err)
	}
	defer unix.IoctlSetInt(fd, unix.TIOCNXCL, 0)
	state.Iflag, state.Oflag, state.Lflag = 0, 0, 0
	state.Cflag = unix.CLOCAL | unix.CREAD | unix.CS8 | baudFlags
	state.Ispeed, state.Ospeed = unix.B115200, unix.B115200
	state.Cc[unix.VMIN], state.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, setTTYFlush, state); err != nil {
		return nil, fmt.Errorf("configure serial port %s: %w", port, err)
	}
	tty := serialPort{fd: fd, deadline: time.Now().Add(5 * time.Second)}
	request := append([]byte("0 "), command...)
	request = append(request, '\n')
	defer clear(request)
	if err := tty.send(request); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(tty)
	skip := false
	for {
		line, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			clear(line)
			skip = true
			continue
		}
		if err != nil {
			clear(line)
			return nil, fmt.Errorf("read USB reply: %w", err)
		}
		if !skip && bytes.HasPrefix(line, []byte("0 ")) {
			out := bytes.Clone(bytes.TrimRight(line[2:], "\r\n"))
			clear(line)
			return out, nil
		}
		clear(line)
		skip = false
	}
}
