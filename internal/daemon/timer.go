package daemon

import (
	"log"
	"slices"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/trigger/timer"
)

// Tick fires each timer Loop and polls each GitHub Loop that is due at now.
// A fire that finds the Loop paused or a Job running is skipped, and the next
// fire is computed from now, so missed fires are never caught up.
func (d *Daemon) Tick(now time.Time) {
	for _, loop := range d.due(now) {
		switch t := loop.Trigger.(type) {
		case config.Timer:
			if _, err := d.Run(loop.Name, ""); err != nil {
				log.Printf("loopd: timer fire of %s skipped: %v", loop.Name, err)
			}
		case config.GitHub:
			d.poll(loop.Name, t)
		}
	}
}

func (d *Daemon) due(now time.Time) []config.Loop {
	d.mu.Lock()
	defer d.mu.Unlock()
	var due []config.Loop
	for name, loop := range d.loops {
		var sched timer.Schedule
		switch t := loop.Trigger.(type) {
		case config.Timer:
			sched = t.Schedule
		case config.GitHub:
			sched = timer.Every(t.Every)
		}
		next, seen := d.next[name]
		if !seen {
			d.next[name] = sched.Next(now)
			continue
		}
		if next.IsZero() || now.Before(next) {
			continue
		}
		d.next[name] = sched.Next(now)
		paused, err := d.st.Paused(name)
		if err != nil {
			log.Printf("loopd: timer fire of %s skipped: %v", name, err)
			continue
		}
		if !paused {
			due = append(due, loop)
		}
	}
	slices.SortFunc(due, func(a, b config.Loop) int { return strings.Compare(a.Name, b.Name) })
	return due
}
