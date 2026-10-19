package timer

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCronNext(t *testing.T) {
	cases := []struct{ expr, after, want string }{
		{"0 9 * * 1-5", "2026-10-19 08:30", "2026-10-19 09:00"},
		{"0 9 * * 1-5", "2026-10-19 09:00", "2026-10-20 09:00"},
		{"0 9 * * 1-5", "2026-10-23 09:00", "2026-10-26 09:00"},
		{"*/15 * * * *", "2026-10-19 10:07", "2026-10-19 10:15"},
		{"30 2 1 * *", "2026-10-19 10:00", "2026-11-01 02:30"},
		{"0 0 13 * 5", "2026-10-19 00:00", "2026-10-23 00:00"},
		{"0 0 13 * 5", "2026-11-08 00:00", "2026-11-13 00:00"},
		{"0 12 * * 7", "2026-10-19 00:00", "2026-10-25 12:00"},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"},
		{"5,10 1-2/1 * * *", "2026-10-19 01:07", "2026-10-19 01:10"},
	}
	for _, c := range cases {
		s, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		if got := s.Next(at(c.after)); !got.Equal(at(c.want)) {
			t.Errorf("%q after %s: got %s, want %s", c.expr, c.after, got.Format("2006-01-02 15:04 Mon"), c.want)
		}
	}
}

func TestParseCronRejects(t *testing.T) {
	for _, expr := range []string{"60 * * * *", "* * *", "a * * * *", "5-1 * * * *", "*/0 * * * *", "* * 0 * *", "* * * 13 *", "* * * * 8"} {
		if _, err := ParseCron(expr); err == nil {
			t.Errorf("%q accepted", expr)
		}
	}
}

func TestEveryNext(t *testing.T) {
	if got := Every(10 * time.Minute).Next(at("2026-10-19 10:07")); !got.Equal(at("2026-10-19 10:17")) {
		t.Fatalf("got %s", got)
	}
}
