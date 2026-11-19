//go:build !windows

package main

import "errors"

func ownerCommand(args []string) (any, error) {
	return nil, errors.New("USB owner commands currently require Windows")
}
