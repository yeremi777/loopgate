package daemon

import (
	"os"
	"path/filepath"
)

// Home is LOOPD_HOME when set, and ~/.loopd otherwise.
func Home() (string, error) {
	if home := os.Getenv("LOOPD_HOME"); home != "" {
		return home, nil
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(user, ".loopd"), nil
}

func Socket(home string) string { return filepath.Join(home, "loopd.sock") }
