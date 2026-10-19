// Package timer computes when a timer Loop fires next.
package timer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule gives the first fire time strictly after a moment, or the zero
// time when it never fires again.
type Schedule interface{ Next(after time.Time) time.Time }

// Every fires a fixed duration after each moment it is asked about.
type Every time.Duration

func (e Every) Next(after time.Time) time.Time { return after.Add(time.Duration(e)) }

// Cron is a parsed five-field cron expression: minute hour day-of-month month
// day-of-week. Only ParseCron builds one.
type Cron struct {
	minute, hour, dom, month, dow uint64
	// Standard cron: when both day fields are restricted, a day matches either.
	domStar, dowStar bool
}

func ParseCron(expr string) (Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Cron{}, fmt.Errorf("cron %q: want 5 fields, got %d", expr, len(fields))
	}
	var c Cron
	var err error
	for i, f := range []struct {
		dst      *uint64
		min, max int
	}{{&c.minute, 0, 59}, {&c.hour, 0, 23}, {&c.dom, 1, 31}, {&c.month, 1, 12}, {&c.dow, 0, 7}} {
		if *f.dst, err = parseField(fields[i], f.min, f.max); err != nil {
			return Cron{}, fmt.Errorf("cron %q: field %d: %w", expr, i+1, err)
		}
	}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1
	}
	c.domStar, c.dowStar = fields[2] == "*", fields[4] == "*"
	return c, nil
}

func parseField(field string, min, max int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step %q", part)
			}
			step = n
		}
		lo, hi := min, max
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = strconv.Atoi(a); err != nil {
				return 0, fmt.Errorf("bad value %q", part)
			}
			hi = lo
			if isRange {
				if hi, err = strconv.Atoi(b); err != nil {
					return 0, fmt.Errorf("bad value %q", part)
				}
			} else if hasStep {
				hi = max
			}
		}
		if lo < min || hi > max || lo > hi {
			return 0, fmt.Errorf("%q is outside %d-%d", part, min, max)
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

// Next steps forward by day, hour, then minute; five years covers every
// expression that fires at all, Feb 29 included.
func (c Cron) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case c.hour&(1<<t.Hour()) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case c.minute&(1<<t.Minute()) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

func (c Cron) dayMatches(t time.Time) bool {
	if c.month&(1<<int(t.Month())) == 0 {
		return false
	}
	dom := c.dom&(1<<t.Day()) != 0
	dow := c.dow&(1<<int(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dow
	case c.dowStar:
		return dom
	}
	return dom || dow
}
