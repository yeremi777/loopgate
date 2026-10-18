package daemon

import (
	"strings"
	"testing"
)

func TestHomeRefusesPathTooLongForTheSocket(t *testing.T) {
	t.Setenv("LOOPD_HOME", "/tmp/"+strings.Repeat("x", 100))
	if _, err := Home(); err == nil || !strings.Contains(err.Error(), "104") {
		t.Fatalf("want error naming the 104-byte limit, got %v", err)
	}
	t.Setenv("LOOPD_HOME", "/tmp/short")
	if home, err := Home(); err != nil || home != "/tmp/short" {
		t.Fatalf("short home: %q, %v", home, err)
	}
}
