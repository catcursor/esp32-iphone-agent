//go:build darwin || linux

package sensephoto

import (
	"net"
	"time"
)

func dialPhoto(addr string) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, 5*time.Second)
}
