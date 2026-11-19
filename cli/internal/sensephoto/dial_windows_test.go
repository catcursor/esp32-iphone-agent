package sensephoto

import (
	"net"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPhotoTCPReceiveBuffer(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := dialPhoto(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw, err := conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var size int
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		size, sockErr = windows.GetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_RCVBUF)
	})
	if err != nil || sockErr != nil || size != 1024 {
		t.Fatalf("photo SO_RCVBUF = %d (control %v, getsockopt %v), want 1024", size, err, sockErr)
	}
}
