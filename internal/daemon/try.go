package daemon

import (
	"errors"

	"github.com/yeremi777/loopgate/internal/store"
)

// ErrNotDone refuses try of a Job that did not end done.
var ErrNotDone = errors.New("only a done job can be tried")

// Tried is a done Job readied to run by hand, and the paths, relative to its
// worktree, of the files copied into it.
type Tried struct {
	Job    store.Job `json:"job"`
	Copied []string  `json:"copied"`
}

// Try copies into a done Job's own worktree each regular file of its Loop's
// checkout that the checkout's .worktreeinclude matches and git ignores.
func (d *Daemon) Try(id string) (Tried, error) {
	return Tried{}, errors.New("not implemented")
}
