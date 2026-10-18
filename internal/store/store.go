// Package store keeps loopd state in SQLite.
package store

import "errors"

type JobState string

const (
	Queued    JobState = "queued"
	Running   JobState = "running"
	Blocked   JobState = "blocked"
	Manual    JobState = "manual"
	Exhausted JobState = "exhausted"
	Failed    JobState = "failed"
	Done      JobState = "done"
)

type Job struct {
	ID    int64
	Loop  string
	State JobState
}

type Store struct{}

// Open opens or creates the state database at path.
func Open(path string) (*Store, error) { return nil, errors.New("not implemented") }

func (s *Store) Close() error { return errors.New("not implemented") }

func (s *Store) SetPaused(loop string, paused bool) error { return errors.New("not implemented") }

func (s *Store) Paused(loop string) (bool, error) { return false, errors.New("not implemented") }

func (s *Store) CreateJob(loop string) (Job, error) { return Job{}, errors.New("not implemented") }

func (s *Store) SetJobState(id int64, state JobState) error { return errors.New("not implemented") }

func (s *Store) Job(id int64) (Job, error) { return Job{}, errors.New("not implemented") }

// ClaimIssue records issue as handled by loop, and reports whether it was new.
func (s *Store) ClaimIssue(loop string, issue int) (bool, error) {
	return false, errors.New("not implemented")
}
