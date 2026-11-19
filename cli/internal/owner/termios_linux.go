package owner

import "golang.org/x/sys/unix"

const getTTY = unix.TCGETS
const setTTYFlush = unix.TCSETSF
const baudFlags = unix.B115200
