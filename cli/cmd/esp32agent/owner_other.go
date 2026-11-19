//go:build !windows && !linux && !darwin

package main

import "errors"

func ownerCommand(args []string) (any, error) {
	return nil, errors.New("USB owner commands require Windows, macOS or Linux")
}
