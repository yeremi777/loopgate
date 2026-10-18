// Package config parses Loop files into typed Loops.
package config

import (
	"errors"
	"time"
)

// Loop is a validated Loop definition. Only Parse builds one.
type Loop struct {
	Name      string
	Checkout  string
	Trigger   Trigger
	Verify    string
	Bounds    Bounds
	Protected []string
	Tests     []string
}

// Trigger is either GitHub or Timer.
type Trigger interface{ isTrigger() }

// GitHub polls a repo for issues that carry Label.
type GitHub struct {
	Repo  string
	Label string
	Every time.Duration
}

// Timer fires on Every or Cron, and runs exactly one of Task or Spec.
type Timer struct {
	Every time.Duration
	Cron  string
	Task  string
	Spec  string
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

// Parse validates a Loop file and returns its Loop.
func Parse(data []byte) (Loop, error) {
	return Loop{}, errors.New("not implemented")
}
