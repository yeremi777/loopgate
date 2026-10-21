package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxSocketPath is macOS's limit on a Unix socket path, terminating NUL included.
const maxSocketPath = 104

// Home is LOOPD_HOME when set, and ~/.loopd otherwise. It refuses a home
// whose socket path the kernel would reject with a bare "invalid argument".
func Home() (string, error) {
	home := os.Getenv("LOOPD_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(user, ".loopd")
	}
	if len(Socket(home)) >= maxSocketPath {
		return "", fmt.Errorf("loopd home %s is too long: its socket path must stay under %d bytes", home, maxSocketPath)
	}
	return home, nil
}

func Socket(home string) string { return filepath.Join(home, "loopd.sock") }

// Tilde writes path with the user's home as ~.
func Tilde(path string) string {
	user, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	user = filepath.Clean(user)
	if path == user {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, user+"/"); ok {
		return "~/" + rel
	}
	return path
}
