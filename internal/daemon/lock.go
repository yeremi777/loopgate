package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lock makes its holder the only loopd for one home directory.
// The kernel drops the flock when the holder exits, so a dead loopd never leaves a stale lock.
type Lock struct{ file *os.File }

// LockedError reports the pid of the loopd that holds the lock.
type LockedError struct{ PID int }

func (e *LockedError) Error() string { return "loopd already running with pid " + strconv.Itoa(e.PID) }

func AcquireLock(home string) (*Lock, error) {
	f, err := os.OpenFile(filepath.Join(home, "loopd.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := claim(f); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &Lock{file: f}, nil
}

func claim(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			data, _ := os.ReadFile(f.Name())
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			return &LockedError{PID: pid}
		}
		return fmt.Errorf("lock: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("write lock pid: %w", err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		return fmt.Errorf("write lock pid: %w", err)
	}
	return nil
}

func (l *Lock) Release() error { return l.file.Close() }
