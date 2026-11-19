//go:build !windows && !darwin && !linux

package owner

import "errors"

const ownerFile = "owner.json"
const credentialSuffix = ""

var unsupported = errors.New("USB owner pairing is supported on Windows, macOS and Linux")

func usbCommand(port string, command []byte) ([]byte, error) { return nil, unsupported }
func readProtected(name string) ([]byte, error)              { return nil, ErrNotPaired }
func writeProtected(name string, data []byte) error          { return unsupported }
