package sensephoto

import (
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func dialPhoto(addr string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second, Control: setPhotoWindow}
	return dialer.Dial("tcp", addr)
}

// Set the advertised receive window before the TCP handshake. Adjusting it on
// an established Windows connection still allowed bursts that stalled the ESP32.
func setPhotoWindow(_, _ string, c syscall.RawConn) error {
	var socketErr error
	err := c.Control(func(fd uintptr) {
		socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_RCVBUF, 1024)
	})
	if err != nil {
		return err
	}
	return socketErr
}
