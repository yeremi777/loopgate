// Package config parses Loop files into typed Loops.
package config

import (
	"bytes"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/yeremi777/loopgate/internal/trigger/timer"
)

// Loop is a validated Loop definition. Only Parse builds one.
type Loop struct {
	Name      string
	Checkout  string
	Trigger   Trigger
	Setup     string
	Verify    string
	Bounds    Bounds
	Protected []string
	Tests     []string
	Network   []string
}

// Trigger is either GitHub or Timer.
type Trigger interface{ isTrigger() }

// GitHub polls a repo for issues that carry Label.
type GitHub struct {
	Repo  string
	Label string
	Every time.Duration
}

// Timer fires on its Schedule, and runs exactly one of Task or Spec.
type Timer struct {
	Schedule timer.Schedule
	Task     string
	Spec     string
}

func (GitHub) isTrigger() {}
func (Timer) isTrigger()  {}

type Bounds struct {
	MaxIterations int
	Timeout       time.Duration
}

// FieldError names the Loop file field that failed validation.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return "loop file: " + e.Field + ": " + e.Reason }

type rawLoop struct {
	Name      string     `yaml:"name"`
	Checkout  string     `yaml:"checkout"`
	Trigger   rawTrigger `yaml:"trigger"`
	Task      string     `yaml:"task"`
	Spec      string     `yaml:"spec"`
	Verify    string     `yaml:"verify"`
	Bounds    rawBounds  `yaml:"bounds"`
	Protected []string   `yaml:"protected"`
	Tests     []string   `yaml:"tests"`
	Network   []string   `yaml:"network"`
}

type rawTrigger struct {
	GitHub *rawGitHub `yaml:"github"`
	Timer  *rawTimer  `yaml:"timer"`
}

type rawGitHub struct {
	Repo  string        `yaml:"repo"`
	Label string        `yaml:"label"`
	Every time.Duration `yaml:"every"`
}

type rawTimer struct {
	Every time.Duration `yaml:"every"`
	Cron  string        `yaml:"cron"`
}

type rawBounds struct {
	MaxIterations int           `yaml:"max_iterations"`
	Timeout       time.Duration `yaml:"timeout"`
}

// A name becomes a file name and part of a branch name.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// A network entry is a domain or a *.domain wildcard, never a URL.
var validDomain = regexp.MustCompile(`^(\*\.)?[a-z0-9-]+(\.[a-z0-9-]+)+$`)

// yaml.v3 reports an unknown field only inside this message text.
var unknownField = regexp.MustCompile(`field (\S+) not found in type`)

// Parse validates a Loop file and returns its Loop.
func Parse(data []byte) (Loop, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var raw rawLoop
	if err := dec.Decode(&raw); err != nil {
		if m := unknownField.FindStringSubmatch(err.Error()); m != nil {
			return Loop{}, &FieldError{Field: m[1], Reason: "unknown field"}
		}
		return Loop{}, errors.New("loop file: " + err.Error())
	}

	if raw.Name == "" {
		return Loop{}, missing("name")
	}
	if !validName.MatchString(raw.Name) {
		return Loop{}, &FieldError{Field: "name", Reason: "must be lowercase letters, digits and dashes"}
	}
	checkout, err := parseCheckout(raw.Checkout)
	if err != nil {
		return Loop{}, err
	}
	trigger, err := parseTrigger(raw)
	if err != nil {
		return Loop{}, err
	}
	if raw.Verify == "" {
		return Loop{}, missing("verify")
	}
	if raw.Bounds.MaxIterations <= 0 {
		return Loop{}, &FieldError{Field: "bounds.max_iterations", Reason: "required, must be positive"}
	}
	if raw.Bounds.Timeout <= 0 {
		return Loop{}, &FieldError{Field: "bounds.timeout", Reason: "required, must be positive"}
	}
	if err := checkGlobs("protected", raw.Protected); err != nil {
		return Loop{}, err
	}
	if err := checkGlobs("tests", raw.Tests); err != nil {
		return Loop{}, err
	}
	network, err := parseNetwork(raw.Network)
	if err != nil {
		return Loop{}, err
	}
	return Loop{
		Name:      raw.Name,
		Checkout:  checkout,
		Trigger:   trigger,
		Verify:    raw.Verify,
		Bounds:    Bounds{MaxIterations: raw.Bounds.MaxIterations, Timeout: raw.Bounds.Timeout},
		Protected: raw.Protected,
		Tests:     raw.Tests,
		Network:   network,
	}, nil
}

// checkGlobs refuses a pattern that path.Match rejects, which would otherwise never match.
func checkGlobs(field string, globs []string) error {
	for _, g := range globs {
		for _, seg := range strings.Split(g, "/") {
			if _, err := path.Match(seg, ""); err != nil {
				return &FieldError{Field: field, Reason: "bad pattern " + g}
			}
		}
	}
	return nil
}

// parseNetwork keeps GitHub out of reach of every stage: loopd alone talks to GitHub.
func parseNetwork(domains []string) ([]string, error) {
	var out []string
	for _, d := range domains {
		d = strings.ToLower(d)
		if !validDomain.MatchString(d) {
			return nil, &FieldError{Field: "network", Reason: "entry " + d + " must be a domain such as proxy.golang.org"}
		}
		for _, banned := range []string{"github.com", "githubusercontent.com"} {
			if d == banned || strings.HasSuffix(d, "."+banned) {
				return nil, &FieldError{Field: "network", Reason: "github domains are not allowed: " + d}
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func parseCheckout(path string) (string, error) {
	if path == "" {
		return "", missing("checkout")
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &FieldError{Field: "checkout", Reason: err.Error()}
		}
		path = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(path) {
		return "", &FieldError{Field: "checkout", Reason: "must be an absolute path or start with ~/"}
	}
	return filepath.Clean(path), nil
}

func parseTrigger(raw rawLoop) (Trigger, error) {
	gh, rt := raw.Trigger.GitHub, raw.Trigger.Timer
	if (gh == nil) == (rt == nil) {
		return nil, &FieldError{Field: "trigger", Reason: "needs exactly one of github or timer"}
	}
	if gh != nil {
		switch {
		case gh.Repo == "":
			return nil, missing("trigger.github.repo")
		case gh.Label == "":
			return nil, missing("trigger.github.label")
		case gh.Every <= 0:
			return nil, &FieldError{Field: "trigger.github.every", Reason: "required, must be positive"}
		case raw.Task != "" || raw.Spec != "":
			return nil, &FieldError{Field: "task", Reason: "a github loop takes its work from issues, not task or spec"}
		}
		return GitHub{Repo: gh.Repo, Label: gh.Label, Every: gh.Every}, nil
	}
	if (rt.Every > 0) == (rt.Cron != "") {
		return nil, &FieldError{Field: "trigger.timer", Reason: "needs exactly one of every or cron"}
	}
	if (raw.Task == "") == (raw.Spec == "") {
		return nil, &FieldError{Field: "task", Reason: "a timer loop needs exactly one of task or spec"}
	}
	var sched timer.Schedule = timer.Every(rt.Every)
	if rt.Cron != "" {
		cron, err := timer.ParseCron(rt.Cron)
		if err != nil {
			return nil, &FieldError{Field: "trigger.timer.cron", Reason: err.Error()}
		}
		if cron.Next(time.Now()).IsZero() {
			return nil, &FieldError{Field: "trigger.timer.cron", Reason: "never fires"}
		}
		sched = cron
	}
	return Timer{Schedule: sched, Task: raw.Task, Spec: raw.Spec}, nil
}

func missing(field string) *FieldError { return &FieldError{Field: field, Reason: "required"} }
