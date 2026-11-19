package owner

import "golang.org/x/sys/unix"

const getTTY = unix.TIOCGETA
const setTTYFlush = unix.TIOCSETAF
const baudFlags = 0 // Darwin uses the separate input/output speed fields.
