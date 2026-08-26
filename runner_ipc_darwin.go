//go:build darwin

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func peerUIDPlatform(fd int) (uint32, error) {
	if _, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID); err != nil {
		return 0, err
	}
	return uint32(os.Geteuid()), nil
}
