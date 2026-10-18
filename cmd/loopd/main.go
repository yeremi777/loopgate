// Command loopd runs Loops in the background and answers loopctl.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/github"
	"github.com/yeremi777/loopgate/internal/ipc"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "loopd:", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := daemon.Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	lock, err := daemon.AcquireLock(home)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := daemon.CheckGH(ctx, "gh"); err != nil {
		return err
	}

	claude, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found on PATH: %w", err)
	}

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.InterruptRunning("loopd stopped while the job ran"); err != nil {
		return err
	}
	cache := filepath.Join(home, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return err
	}
	r := runner.Runner{Claude: claude, Environ: os.Environ(), Cache: cache}
	d, err := daemon.New(home, st, daemon.Deps{
		Pipeline: job.Pipeline{Stages: r, Shell: r, Publisher: github.Publisher{GH: "gh"}},
		Notify:   notify,
	})
	if err != nil {
		return err
	}
	defer d.Close()

	sock := daemon.Socket(home)
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return errors.Join(err, ln.Close())
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	if err := ipc.Serve(ln, d); !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// notify shows a macOS notification. The text goes in as arguments, never
// as AppleScript source, so a question from a Job cannot inject script.
func notify(title, message string) {
	cmd := exec.Command("osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		title, message)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "loopd: notify: %v: %s\n", err, out)
	}
}
