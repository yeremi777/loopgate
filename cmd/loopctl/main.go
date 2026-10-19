// Command loopctl controls a running loopd.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/feed"
	"github.com/yeremi777/loopgate/internal/ipc"
	"github.com/yeremi777/loopgate/internal/service"
	"github.com/yeremi777/loopgate/internal/store"
	"github.com/yeremi777/loopgate/skills"
	"golang.org/x/sys/unix"
)

const usage = `loopctl controls loopd, which runs Claude Code on loops and stops at a draft PR.

usage:
  loopctl list                         show each loop with the job id of each of its jobs in progress and of the 5 newest ended
  loopctl add <loop.yaml>              register a loop file
  loopctl remove <loop>                unregister a loop
  loopctl pause <loop>                 stop a loop's timer or issue polling
  loopctl resume <loop>                start them again
  loopctl run <loop> [--spec <path>]   start a job now on --spec, or a timer loop's own task or spec
  loopctl stop <loop>                  end the loop's running job; the loop stays
  loopctl logs <job>                   show a job's state, branch, PR, cost and progress
  loopctl logs -f <job>                show that, then follow the running job until it ends; Ctrl-C leaves it running
  loopctl takeover <job>               take a job, stopping it if it runs, and hand its worktree to you
  loopctl handback <job>               give a taken-over job back to its loop
  loopctl requeue <loop> <issue>       let a github loop run an issue again
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
		jobs, err := c.Jobs(ctx)
		if err != nil {
			return err
		}
		_, notTerminal := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
		printList(loops, jobs, notTerminal == nil && os.Getenv("NO_COLOR") == "")
		return nil
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
	case cmd == "logs" && len(rest) == 2 && rest[0] == "-f":
		id, err := jobID(rest[1])
		if err != nil {
			return err
		}
		log, err := c.Job(ctx, id)
		if err != nil {
			return err
		}
		printLog(log)
		fmt.Println()
		width := termWidth()
		end, err := c.Follow(ctx, id, func(e feed.Event) {
			fmt.Println(cut(e.Time.Local().Format("15:04:05")+" "+e.Stage+"  "+printable(e.Text), width))
		})
		if err != nil {
			return err
		}
		printEnd(end)
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

// printEnd prints how a followed Job ended: its PR, question, error, or the
// first line of its summary.
func printEnd(j store.Job) {
	summary, _, _ := strings.Cut(j.Summary, "\n")
	line := fmt.Sprintf("job %d %s", j.ID, j.State)
	for _, detail := range []string{j.PRURL, j.Question, j.Error, summary} {
		if detail != "" {
			line += ": " + detail
			break
		}
	}
	for l := range strings.Lines(line) {
		fmt.Println(printable(strings.TrimSuffix(l, "\n")))
	}
}

// listBlock is one Loop of loopctl list with its Jobs, newest first.
type listBlock struct {
	name, state, trigger, checkout string
	jobs                           []store.Job
}

// printList prints each Loop, sorted by name, as a block with its Jobs; a
// Job whose Loop was removed gets a block of its own. color paints names,
// headers and states without changing any column's width.
func printList(loops []daemon.LoopStatus, jobs []store.Job, color bool) {
	var blocks []*listBlock
	byName := map[string]*listBlock{}
	for _, l := range loops {
		b := &listBlock{name: l.Name, state: "active", trigger: l.Trigger, checkout: home(l.Checkout)}
		if l.Paused {
			b.state = "paused"
		}
		blocks = append(blocks, b)
		byName[l.Name] = b
	}
	for _, j := range jobs {
		b, ok := byName[j.Loop]
		if !ok {
			b = &listBlock{name: j.Loop, state: "removed"}
			blocks = append(blocks, b)
			byName[j.Loop] = b
		}
		b.jobs = append(b.jobs, j)
	}
	if len(blocks) == 0 {
		fmt.Println("no loops; add one with loopctl add <loop.yaml>")
		return
	}
	slices.SortFunc(blocks, func(a, b *listBlock) int { return strings.Compare(a.name, b.name) })
	nameWidth := 0
	for _, b := range blocks {
		nameWidth = max(nameWidth, utf8.RuneCountInString(b.name))
	}
	paint := func(code, s string) string {
		if !color || s == "" {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	for i, b := range blocks {
		if i > 0 {
			fmt.Println()
		}
		line := paint("1", b.name) + strings.Repeat(" ", nameWidth-utf8.RuneCountInString(b.name)) + "  " + paint(stateColor[b.state], b.state)
		if b.trigger != "" {
			line += "  " + b.trigger
		}
		fmt.Println(line)
		if b.checkout != "" {
			fmt.Println("  checkout: " + b.checkout)
		}
		if len(b.jobs) == 0 {
			fmt.Println("  no jobs")
			continue
		}
		rows := [][]string{{"JOB", "STATE", "WORK", "RESULT"}}
		for _, j := range b.jobs {
			rows = append(rows, []string{strconv.FormatInt(j.ID, 10), string(j.State), cell(j.Work), cell(result(j))})
		}
		widths := make([]int, len(rows[0]))
		for _, row := range rows {
			for c, text := range row {
				widths[c] = max(widths[c], utf8.RuneCountInString(text))
			}
		}
		for r, row := range rows {
			line := " "
			for c, text := range row {
				switch {
				case r == 0:
					text = paint("2", text)
				case c == 1:
					text = paint(stateColor[row[1]], text)
				}
				line += " " + text
				if c < len(row)-1 {
					line += strings.Repeat(" ", widths[c]-utf8.RuneCountInString(row[c])+1)
				}
			}
			fmt.Println(strings.TrimRight(line, " "))
		}
	}
}

// stateColor is the terminal color of each Loop and Job state.
var stateColor = map[string]string{
	"active": "32", "done": "32",
	"paused": "36", "queued": "36", "running": "36",
	"blocked": "33", "manual": "33",
	"failed": "31", "exhausted": "31", "removed": "31",
}

// result is what a Job's row says about how it stands: its PR, question,
// error or summary.
func result(j store.Job) string {
	switch j.State {
	case store.Done:
		return j.PRURL
	case store.Blocked:
		return j.Question
	case store.Failed:
		return j.Error
	case store.Exhausted:
		return j.Summary
	case store.Manual:
		return "taken over"
	}
	return ""
}

// home writes path with the user's home as ~.
func home(path string) string {
	user, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	user = filepath.Clean(user)
	if path == user {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, user+"/"); ok {
		return "~/" + rel
	}
	return path
}

// cell is the first line of s as one table cell of at most 60 characters.
func cell(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return cut(printable(first), 60)
}

// printable replaces each control and bidi control character in s, which a
// model or a repository wrote, so it cannot move the cursor, reorder the
// line, or send the terminal a command; a tab becomes a space.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r):
			return '�'
		}
		return r
	}, s)
}

// termWidth is stdout's terminal width, or 0 when stdout is not a terminal.
func termWidth() int {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0
	}
	return int(ws.Col)
}

// cut shortens line to width characters, ending it in "…"; width 0 keeps it whole.
func cut(line string, width int) string {
	r := []rune(line)
	if width <= 0 || len(r) <= width {
		return line
	}
	return string(r[:width-1]) + "…"
}
