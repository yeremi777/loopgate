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
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

type LoopStatus struct {
	Name     string `json:"name"`
	Paused   bool   `json:"paused"`
	Trigger  string `json:"trigger"`
	Checkout string `json:"checkout"`
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
	// current is the one Job that holds the running slot, or nil.
	current *running
	// next holds each timer Loop's next fire, set when Tick first sees it.
	next map[string]time.Time
	// fetching keeps a Job start and a Trial gate from fetching origin into
	// one checkout at once, which fails on git's ref locks.
	fetching sync.Mutex

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
	// A trial's worktree outlives only a loopd that was killed during it.
	if err := os.RemoveAll(filepath.Join(home, "trials")); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{home: home, dir: dir, st: st, loops: loops, deps: deps, next: map[string]time.Time{}, ctx: ctx, cancel: cancel}, nil
}

// Add registers a Loop once its checkout checks and Trial gate pass. Ending
// ctx, as when loopctl goes away, stops the Trial gate and adds nothing.
func (d *Daemon) Add(ctx context.Context, data []byte) (config.Loop, error) {
	loop, err := config.Parse(data)
	if err != nil {
		return config.Loop{}, err
	}
	if err := d.exists(loop.Name); err != nil {
		return config.Loop{}, err
	}
	if err := d.admit(); err != nil {
		return config.Loop{}, err
	}
	defer d.jobs.Done()
	if err := checkCheckout(ctx, loop); err != nil {
		return config.Loop{}, err
	}
	if err := d.trial(ctx, loop); err != nil {
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

// exists fails with ErrLoopExists when name is taken, before a slow check
// that Add would otherwise waste; Add checks again under the lock.
func (d *Daemon) exists(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.loops[name]; ok {
		return fmt.Errorf("%s: %w", name, ErrLoopExists)
	}
	return nil
}

// admit counts an Add in the work that Close waits for, unless Close began.
func (d *Daemon) admit() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ctx.Err(); err != nil {
		return fmt.Errorf("loopd is stopping: %w", err)
	}
	d.jobs.Add(1)
	return nil
}

func (d *Daemon) createWorktree(ctx context.Context, w runner.Worktree) (runner.Created, error) {
	d.fetching.Lock()
	defer d.fetching.Unlock()
	return runner.CreateWorktree(ctx, w)
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
	delete(d.next, name)
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
		out = append(out, LoopStatus{Name: name, Paused: paused, Trigger: triggerName(d.loops[name].Trigger), Checkout: d.loops[name].Checkout})
	}
	slices.SortFunc(out, func(a, b LoopStatus) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Jobs are the Jobs in progress and the newest that ended.
func (d *Daemon) Jobs() ([]store.Job, error) { return d.st.Jobs() }

// Resolve is the id of the one Job whose id starts with prefix, in any letter case.
func (d *Daemon) Resolve(prefix string) (string, error) { return d.st.Resolve(prefix) }

func triggerName(t config.Trigger) string {
	if gh, ok := t.(config.GitHub); ok {
		return "github " + gh.Repo
	}
	return "timer"
}

func (d *Daemon) path(name string) string { return filepath.Join(d.dir, name+".yaml") }
