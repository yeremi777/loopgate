// Package daemon owns the Loops of one loopd and the operations on them.
package daemon

import (
	"context"
	"errors"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/store"
)

type LoopStatus struct {
	Name   string `json:"name"`
	Paused bool   `json:"paused"`
}

type Daemon struct{}

// New loads the Loop files in home/loops.
func New(home string, st *store.Store) (*Daemon, error) { return nil, errors.New("not implemented") }

func (d *Daemon) Add(data []byte) (config.Loop, error) {
	return config.Loop{}, errors.New("not implemented")
}

func (d *Daemon) Remove(name string) error { return errors.New("not implemented") }

func (d *Daemon) SetPaused(name string, paused bool) error { return errors.New("not implemented") }

func (d *Daemon) List() ([]LoopStatus, error) { return nil, errors.New("not implemented") }

// CheckGH fails unless the gh binary reports a logged-in user.
func CheckGH(ctx context.Context, gh string) error { return errors.New("not implemented") }
