// Command loopctl controls a running loopd.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/ipc"
	"github.com/yeremi777/loopgate/internal/service"
	"github.com/yeremi777/loopgate/skills"
)

const usage = `loopctl controls loopd, which runs Claude Code on loops and stops at a draft PR.

usage:
  loopctl list                         show each loop's state, trigger and checkout
  loopctl add <loop.yaml>              register a loop file
  loopctl remove <name>                unregister a loop
  loopctl pause <name>                 stop a loop's timer or issue polling
  loopctl resume <name>                start them again
  loopctl run <name> [--spec <path>]   start a job now on --spec, or a timer loop's own task or spec
  loopctl stop <name>                  end the loop's running job; the loop stays
  loopctl logs <job>                   show a job's state, branch, PR, cost and progress
  loopctl takeover <job>               take a job, stopping it if it runs, and hand its worktree to you
  loopctl handback <job>               give a taken-over job back to its loop
  loopctl requeue <name> <issue>       let a github loop run an issue again
  loopctl install                      start loopd now and at login, and add the Claude Code skill
  loopctl uninstall                    stop loopd and remove the agent and the skill
  loopctl version                      print the installed loopgate version`

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
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	case "version", "--version":
		fmt.Println("loopgate " + version())
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "install" && len(rest) == 0:
		return install(home)
	case cmd == "uninstall" && len(rest) == 0:
		return uninstall()
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
		fmt.Println("checking the checkout, then running the trial gate on origin's default branch")
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

// version is the module version that Go stamped into the binary: the release
// tag for go install @version, else a pseudo-version naming the commit.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(unknown)"
	}
	return info.Main.Version
}

func jobID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("job id %q is not a number", s)
	}
	return id, nil
}

// install refuses until the stage skills that every job calls are installed,
// then writes the launchd agent for the loopd that sits next to this loopctl,
// starts it, and installs the loopgate skill for Claude Code.
func install(home string) error {
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
	var missing []string
	for _, name := range []string{"drill", "implement", "rubric", "land", "tldr"} {
		if _, err := os.Stat(filepath.Join(user, ".claude", "skills", name, "SKILL.md")); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing stage skills in ~/.claude/skills: %s; run `npx skills add pwguler/skills -g -a claude-code`", strings.Join(missing, ", "))
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	path := service.ResolvePath(os.Getenv("PATH"))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, tool := range []string{"claude", "gh", "git", "node", "go"} {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", tool, lookIn(path, tool))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	plist, err := service.Install(agentsDir(user), service.Config{Loopd: loopd, Home: home, Path: path})
	if err != nil {
		return err
	}
	log := filepath.Join(home, "loopd.log")
	var logged int64
	if info, err := os.Stat(log); err == nil {
		logged = info.Size()
	}
	if err := service.Load(plist); err != nil {
		return err
	}
	dir := skillDir(user)
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skills.Loopgate, 0o644); err != nil {
		return err
	}
	if err := awaitLoopd(home, log, logged); err != nil {
		return err
	}
	fmt.Printf("loopd answers now and starts at every login; its log is %s\nthe loopgate skill is in %s\nnext: copy a loop file from examples/ and run loopctl add <file>\n",
		log, dir)
	return nil
}

// lookIn finds name in the directories of path, as the shell would.
func lookIn(path, name string) string {
	for _, dir := range filepath.SplitList(path) {
		file := filepath.Join(dir, name)
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return file
		}
	}
	return "not found"
}

// awaitLoopd waits until loopd answers on its socket, and fails with the
// error loopd wrote to its log after offset when it exits first. loopd's gh
// check alone may take 15s.
func awaitLoopd(home, log string, offset int64) error {
	c := ipc.NewClient(daemon.Socket(home))
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := c.List(context.Background()); err == nil {
			return nil
		}
		if msg := loggedError(log, offset); msg != "" {
			return fmt.Errorf("loopd did not start: %s", msg)
		}
	}
	return fmt.Errorf("loopd did not answer within 30s; see %s", log)
}

// loggedError is the last error loopd wrote to log after offset: its
// "loopd: " line and the lines after it.
func loggedError(log string, offset int64) string {
	data, err := os.ReadFile(log)
	if err != nil || int64(len(data)) <= offset {
		return ""
	}
	tail := "\n" + string(data[offset:])
	i := strings.LastIndex(tail, "\nloopd: ")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(tail[i+len("\nloopd: "):])
}

// uninstall stops loopd and removes what install added. Loops, jobs and
// worktrees in the loopd home stay.
func uninstall() error {
	user, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	service.Unload()
	plist := filepath.Join(agentsDir(user), service.Label+".plist")
	if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.RemoveAll(skillDir(user)); err != nil {
		return err
	}
	fmt.Println("loopd stopped; the agent and the loopgate skill are removed")
	return nil
}

func agentsDir(user string) string { return filepath.Join(user, "Library", "LaunchAgents") }

func skillDir(user string) string { return filepath.Join(user, ".claude", "skills", "loopgate") }

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
