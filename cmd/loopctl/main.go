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
	"github.com/yeremi777/loopgate/internal/service"
)

const usage = `loopctl controls loopd, which runs Claude Code on loops and stops at a draft PR.

usage:
  loopctl list                         show loops and whether each is paused
  loopctl add <loop.yaml>              register a loop file
  loopctl remove <name>                unregister a loop
  loopctl pause <name>                 stop a loop's timer or issue polling
  loopctl resume <name>                start them again
  loopctl run <name> [--spec <path>]   start a job now, on a spec or the loop's own task
  loopctl stop <name>                  end the loop's running job; the loop stays
  loopctl logs <job>                   show a job's state, branch, PR, cost and progress
  loopctl takeover <job>               stop a job and hand its worktree to you
  loopctl handback <job>               give a taken-over job back to its loop
  loopctl requeue <name> <issue>       let a github loop run an issue again
  loopctl install-service              start loopd at login with launchd`

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
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "install-service" && len(rest) == 0:
		return installService(home)
	case cmd == "list" && len(rest) == 0:
		loops, err := c.List(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tSTATE\tTRIGGER\tCHECKOUT")
		for _, l := range loops {
			state := "active"
			if l.Paused {
				state = "paused"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Name, state, l.Trigger, l.Checkout)
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
	case cmd == "requeue" && len(rest) == 2:
		n, err := strconv.Atoi(rest[1])
		if err != nil {
			return fmt.Errorf("issue %q is not a number", rest[1])
		}
		return c.Requeue(ctx, rest[0], n)
	case cmd == "stop" && len(rest) == 1:
		return c.Stop(ctx, rest[0])
	case cmd == "takeover" && len(rest) == 1:
		id, err := jobID(rest[0])
		if err != nil {
			return err
		}
		j, err := c.Takeover(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("job %d is yours on %s; the loop stays off it until handback.\n  cd %s\n", j.ID, j.Branch, j.Worktree)
		return nil
	case cmd == "handback" && len(rest) == 1:
		id, err := jobID(rest[0])
		if err != nil {
			return err
		}
		j, err := c.Handback(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("job %d is back with the loop on %s\n", j.ID, j.Branch)
		return nil
	case cmd == "logs" && len(rest) == 1:
		id, err := jobID(rest[0])
		if err != nil {
			return err
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

func jobID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("job id %q is not a number", s)
	}
	return id, nil
}

// installService writes the launchd agent for the loopd that sits next to
// this loopctl, and says how to start it.
func installService(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	loopd := filepath.Join(filepath.Dir(exe), "loopd")
	if _, err := os.Stat(loopd); err != nil {
		return fmt.Errorf("loopd must sit next to loopctl: %w", err)
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	path, err := service.Install(filepath.Join(user, "Library", "LaunchAgents"), service.Config{Loopd: loopd, Home: home, Path: os.Getenv("PATH")})
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s\nstart it now and at every login with:\n  launchctl bootstrap gui/%d %s\nlogs: %s\n",
		path, os.Getuid(), path, filepath.Join(home, "loopd.log"))
	return nil
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
