//go:build windows

package herdr

import (
	"context"
	"io"
	"os"
)

// On Windows HERDR_SOCKET_PATH names a file, and herdr's pipe carries that whole path as its name.
func dial(_ context.Context, path string) (io.ReadWriteCloser, error) {
	return os.OpenFile(`\\.\pipe\`+path, os.O_RDWR, 0)
}
