package runner

import (
	"context"
	"errors"
)

// Worktree describes a Job worktree cut from the default branch of Checkout's origin.
// Spec, when set, is a path relative to Checkout that is copied into the worktree.
type Worktree struct {
	Checkout string
	Path     string
	Branch   string
	Spec     string
}

func CreateWorktree(ctx context.Context, w Worktree) error { return errors.New("not implemented") }
