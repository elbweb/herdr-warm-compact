//go:build !windows

package herdr

import (
	"fmt"
	"os"
	"syscall"
)

// On linux and macOS HERDR_SOCKET_PATH is the socket itself; a new server creates a new socket file, so
// its inode and modification time name the server.
func serverIdentity(socket string) string {
	st, err := os.Stat(socket)
	if err != nil {
		return ""
	}
	var ino uint64
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		ino = uint64(s.Ino)
	}
	return fmt.Sprintf("%d:%d", ino, st.ModTime().UnixNano())
}
