package daemon

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

const lockHolderEnv = "LOOPGATE_TEST_LOCK_HOLDER"

func TestMain(m *testing.M) {
	if home := os.Getenv(lockHolderEnv); home != "" {
		if _, err := AcquireLock(home); err != nil {
			os.Exit(2)
		}
		if _, err := os.Stdout.WriteString("locked\n"); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLockRefusesSecondOwnerAndNamesPID(t *testing.T) {
	home := t.TempDir()
	holder := startLockHolder(t, home)

	_, err := AcquireLock(home)
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("want LockedError, got %v", err)
	}
	if locked.PID != holder.Process.Pid {
		t.Fatalf("want pid %d, got %d", holder.Process.Pid, locked.PID)
	}
}

func TestLockOfDeadOwnerIsTakenOver(t *testing.T) {
	home := t.TempDir()
	holder := startLockHolder(t, home)
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()

	lock, err := AcquireLock(home)
	if err != nil {
		t.Fatalf("want lock after owner died, got %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func startLockHolder(t *testing.T, home string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), lockHolderEnv+"="+home)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("lock holder did not lock: %q %v", line, err)
	}
	return cmd
}
