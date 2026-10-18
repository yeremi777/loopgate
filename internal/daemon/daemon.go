// Package daemon owns the Loops of one loopd and the operations on them.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/store"
)

type LoopStatus struct {
	Name   string `json:"name"`
	Paused bool   `json:"paused"`
}

var (
	ErrLoopExists  = errors.New("loop already exists")
	ErrUnknownLoop = errors.New("unknown loop")
)

// Daemon owns the Loop files in its loops directory, their state in the
// store, and the one Job that may run at a time.
type Daemon struct {
	mu    sync.Mutex
	home  string
	dir   string
	st    *store.Store
	loops map[string]config.Loop
	deps  Deps
	busy  bool

	ctx    context.Context
	cancel context.CancelFunc
	jobs   sync.WaitGroup
}

// New loads the Loop files in home/loops.
func New(home string, st *store.Store, deps Deps) (*Daemon, error) {
	dir := filepath.Join(home, "loops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	loops := make(map[string]config.Loop, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		loop, err := config.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		loops[loop.Name] = loop
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{home: home, dir: dir, st: st, loops: loops, deps: deps, ctx: ctx, cancel: cancel}, nil
}

func (d *Daemon) Add(data []byte) (config.Loop, error) {
	loop, err := config.Parse(data)
	if err != nil {
		return config.Loop{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.loops[loop.Name]; ok {
		return config.Loop{}, fmt.Errorf("%s: %w", loop.Name, ErrLoopExists)
	}
	if err := d.st.SetPaused(loop.Name, false); err != nil {
		return config.Loop{}, err
	}
	tmp := d.path(loop.Name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return config.Loop{}, err
	}
	if err := os.Rename(tmp, d.path(loop.Name)); err != nil {
		return config.Loop{}, err
	}
	d.loops[loop.Name] = loop
	return loop, nil
}

func (d *Daemon) Remove(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.loops[name]; !ok {
		return fmt.Errorf("%s: %w", name, ErrUnknownLoop)
	}
	if err := os.Remove(d.path(name)); err != nil {
		return err
	}
	delete(d.loops, name)
	return nil
}

func (d *Daemon) SetPaused(name string, paused bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.loops[name]; !ok {
		return fmt.Errorf("%s: %w", name, ErrUnknownLoop)
	}
	return d.st.SetPaused(name, paused)
}

// List returns every Loop, sorted by name.
func (d *Daemon) List() ([]LoopStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]LoopStatus, 0, len(d.loops))
	for name := range d.loops {
		paused, err := d.st.Paused(name)
		if err != nil {
			return nil, err
		}
		out = append(out, LoopStatus{Name: name, Paused: paused})
	}
	slices.SortFunc(out, func(a, b LoopStatus) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (d *Daemon) path(name string) string { return filepath.Join(d.dir, name+".yaml") }
