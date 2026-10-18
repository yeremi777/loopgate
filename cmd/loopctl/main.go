// Command loopctl controls a running loopd.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/ipc"
)

const usage = `usage:
  loopctl list
  loopctl add <loop.yaml>
  loopctl remove <name>
  loopctl pause <name>
  loopctl resume <name>
  loopctl run <name> [--spec <path>]
  loopctl logs <job>`

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
	case cmd == "run" && (len(rest) == 1 || len(rest) == 3 && rest[1] == "--spec"):
		var spec string
		if len(rest) == 3 {
			if spec, err = filepath.Abs(rest[2]); err != nil {
				return err
			}
		}
		j, err := c.Run(ctx, rest[0], spec)
		if err != nil {
			return err
		}
		fmt.Printf("job %d running on %s in %s\n", j.ID, j.Branch, j.Worktree)
		return nil
	case cmd == "logs" && len(rest) == 1:
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			return fmt.Errorf("job id %q is not a number", rest[0])
		}
		log, err := c.Job(ctx, id)
		if err != nil {
			return err
		}
		printLog(log)
		return nil
	default:
		return errors.New(usage)
	}
}

func printLog(log daemon.JobLog) {
	j := log.Job
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 1, ' ', 0)
	row := func(name, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(w, "%s:\t%s\n", name, value)
		}
	}
	row("job", fmt.Sprintf("%d of loop %s", j.ID, j.Loop))
	row("state", string(j.State))
	row("work", j.Work)
	row("branch", j.Branch)
	row("worktree", j.Worktree)
	row("pr", j.PRURL)
	row("cost", fmt.Sprintf("$%.2f", j.CostUSD))
	row("question", j.Question)
	row("error", j.Error)
	_ = w.Flush()
	if j.Summary != "" {
		fmt.Printf("\nsummary:\n%s\n", j.Summary)
	}
	if log.Progress != "" {
		fmt.Printf("\nprogress:\n%s", log.Progress)
	}
}
