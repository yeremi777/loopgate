package daemon

import (
	"log"
	"slices"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
)

// Tick runs each timer Loop whose fire is due at now. A fire that finds the
// Loop paused or a Job running is skipped, and the next fire is computed from
// now, so missed fires are never caught up.
func (d *Daemon) Tick(now time.Time) {
	for _, name := range d.due(now) {
		if _, err := d.Run(name, ""); err != nil {
			log.Printf("loopd: timer fire of %s skipped: %v", name, err)
		}
	}
}

func (d *Daemon) due(now time.Time) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var names []string
	for name, loop := range d.loops {
		t, ok := loop.Trigger.(config.Timer)
		if !ok {
			continue
		}
		next, seen := d.next[name]
		if !seen {
			d.next[name] = t.Schedule.Next(now)
			continue
		}
		if next.IsZero() || now.Before(next) {
			continue
		}
		d.next[name] = t.Schedule.Next(now)
		paused, err := d.st.Paused(name)
		if err != nil {
			log.Printf("loopd: timer fire of %s skipped: %v", name, err)
			continue
		}
		if !paused {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
