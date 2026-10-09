//go:build !windows

package herdr

import (
	"context"
	"io"
	"net"
)

func dial(ctx context.Context, path string) (io.ReadWriteCloser, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
