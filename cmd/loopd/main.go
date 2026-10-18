// Command loopd runs Loops in the background and answers loopctl.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/ipc"
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

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	d, err := daemon.New(home, st)
	if err != nil {
		return err
	}

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
