//go:build !windows

package owner

import "errors"

var unsupported = errors.New("USB owner pairing and secure profile storage are supported on Windows")

func usbCommand(port string, command []byte) ([]byte, error) { return nil, unsupported }
func readProtected(name string) ([]byte, error)              { return nil, ErrNotPaired }
func writeProtected(name string, data []byte) error          { return unsupported }
