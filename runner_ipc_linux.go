//go:build linux

package main

import (
	"golang.org/x/sys/unix"
)

func peerUIDPlatform(fd int) (uint32, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
