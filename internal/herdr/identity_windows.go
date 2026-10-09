//go:build windows

package herdr

import (
	"os"
	"strings"
)

// On Windows HERDR_SOCKET_PATH is a file holding <pid>:<start-ns>, which names the running server.
func serverIdentity(socket string) string {
	b, err := os.ReadFile(socket)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
