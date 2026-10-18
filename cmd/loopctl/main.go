// Command loopctl controls a running loopd.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/ipc"
)

const usage = `usage:
  loopctl list
  loopctl add <loop.yaml>
  loopctl remove <name>
  loopctl pause <name>
  loopctl resume <name>`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "loopctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	home, err := daemon.Home()
	if err != nil {
		return err
	}
	c := ipc.NewClient(daemon.Socket(home))
	ctx := context.Background()
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "list" && len(rest) == 0:
		loops, err := c.List(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tSTATE")
		for _, l := range loops {
			state := "active"
			if l.Paused {
				state = "paused"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\n", l.Name, state)
		}
		return w.Flush()
	case cmd == "add" && len(rest) == 1:
		data, err := os.ReadFile(rest[0])
		if err != nil {
			return err
		}
		name, err := c.Add(ctx, data)
		if err != nil {
			return err
		}
		fmt.Println("added", name)
		return nil
	case cmd == "remove" && len(rest) == 1:
		return c.Remove(ctx, rest[0])
	case cmd == "pause" && len(rest) == 1:
		return c.Pause(ctx, rest[0])
	case cmd == "resume" && len(rest) == 1:
		return c.Resume(ctx, rest[0])
	default:
		return errors.New(usage)
	}
}
