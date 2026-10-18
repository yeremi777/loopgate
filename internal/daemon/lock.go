package daemon

import (
	"errors"
	"strconv"
)

// Lock makes its holder the only loopd for one home directory.
type Lock struct{}

// LockedError reports the pid of the loopd that holds the lock.
type LockedError struct{ PID int }

func (e *LockedError) Error() string { return "loopd already running with pid " + strconv.Itoa(e.PID) }

func AcquireLock(home string) (*Lock, error) { return nil, errors.New("not implemented") }

func (l *Lock) Release() error { return errors.New("not implemented") }
