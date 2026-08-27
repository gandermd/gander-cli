//go:build darwin

package main

import (
	"os"
)

// peerUIDPlatform returns os.Geteuid() unconditionally on macOS.
//
// On macOS the socket-level LOCAL_PEERPID getsockopt is unreliable for
// connections that have just been accepted (it returns "socket is not
// connected" intermittently, depending on the dispatch order inside
// the kernel). The UDS file is created with mode 0600 inside
// ~/.gander (which is mode 0700), so only same-user processes can
// connect in practice -- the peer-credential check is redundant on
// this OS. Linux's SO_PEERCRED path in runner_ipc_linux.go is the
// actual defense; macOS relies on the directory and file modes.
//
// Same-UID detection here is therefore a no-op rather than a true
// syscall: any other process able to reach the daemon over the UDS
// has already passed macOS's UDS file-permission gate.
func peerUIDPlatform(_ int) (uint32, error) {
	return uint32(os.Geteuid()), nil
}
