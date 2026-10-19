// Package store keeps loopd state in SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

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
	ID       int64
	Loop     string
	State    JobState
	Work     string
	Branch   string
	Worktree string
	Base     string
	Issue    int
	Question string
	Summary  string
	PRURL    string
	CostUSD  float64
	Error    string
}

// Finish is how one run of a Job ended. CostUSD adds to the cost of earlier runs.
type Finish struct {
	State    JobState
	Question string
	Summary  string
	PRURL    string
	CostUSD  float64
	Error    string
}

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS loops (
	name   TEXT PRIMARY KEY,
	paused INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS jobs (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	loop     TEXT NOT NULL,
	state    TEXT NOT NULL,
	work     TEXT NOT NULL DEFAULT '',
	branch   TEXT NOT NULL DEFAULT '',
	worktree TEXT NOT NULL DEFAULT '',
	base     TEXT NOT NULL DEFAULT '',
	issue    INTEGER NOT NULL DEFAULT 0,
	question TEXT NOT NULL DEFAULT '',
	summary  TEXT NOT NULL DEFAULT '',
	pr_url   TEXT NOT NULL DEFAULT '',
	cost_usd REAL NOT NULL DEFAULT 0,
	error    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS issues (
	loop   TEXT NOT NULL,
	number INTEGER NOT NULL,
	PRIMARY KEY (loop, number)
);`

// Open opens or creates the state database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, errors.Join(fmt.Errorf("create schema: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SetPaused(loop string, paused bool) error {
	_, err := s.db.Exec(`INSERT INTO loops (name, paused) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET paused = excluded.paused`, loop, paused)
	return err
}

func (s *Store) Paused(loop string) (bool, error) {
	var paused bool
	err := s.db.QueryRow(`SELECT paused FROM loops WHERE name = ?`, loop).Scan(&paused)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return paused, err
}

func (s *Store) CreateJob(loop string) (Job, error) {
	res, err := s.db.Exec(`INSERT INTO jobs (loop, state) VALUES (?, ?)`, loop, Queued)
	if err != nil {
		return Job{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Job{}, err
	}
	return Job{ID: id, Loop: loop, State: Queued}, nil
}

func (s *Store) SetJobState(id int64, state JobState) error {
	return s.updateJob(id, `UPDATE jobs SET state = ? WHERE id = ?`, state, id)
}

// Start is where a Job runs: its work, branch, worktree, the commit it was
// cut from, and the issue it answers (0 for none).
type Start struct {
	Work     string
	Branch   string
	Worktree string
	Base     string
	Issue    int
}

// StartJob marks a Job running in its worktree.
func (s *Store) StartJob(id int64, st Start) error {
	return s.updateJob(id, `UPDATE jobs SET state = ?, work = ?, branch = ?, worktree = ?, base = ?, issue = ? WHERE id = ?`,
		Running, st.Work, st.Branch, st.Worktree, st.Base, st.Issue, id)
}

func (s *Store) FinishJob(id int64, f Finish) error {
	return s.updateJob(id, `UPDATE jobs SET state = ?, question = ?, summary = ?, pr_url = ?, cost_usd = cost_usd + ?, error = ? WHERE id = ?`,
		f.State, f.Question, f.Summary, f.PRURL, f.CostUSD, f.Error, id)
}

// InterruptRunning fails every queued or running Job, which a dead loopd left behind.
func (s *Store) InterruptRunning(reason string) error {
	_, err := s.db.Exec(`UPDATE jobs SET state = ?, error = ? WHERE state IN (?, ?)`, Failed, reason, Queued, Running)
	return err
}

func (s *Store) updateJob(id int64, query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, fmt.Errorf("job %d: %w", id, ErrNotFound))
	}
	return nil
}

const jobColumns = `id, loop, state, work, branch, worktree, base, issue, question, summary, pr_url, cost_usd, error`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.Loop, &job.State, &job.Work, &job.Branch, &job.Worktree, &job.Base, &job.Issue, &job.Question, &job.Summary, &job.PRURL, &job.CostUSD, &job.Error)
	return job, err
}

func (s *Store) Job(id int64) (Job, error) {
	job, err := scanJob(s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("job %d: %w", id, ErrNotFound)
	}
	return job, err
}

// Jobs are every Job in progress, that is queued, running, Blocked or
// manual, and the five newest that ended done, failed or exhausted, newest first.
func (s *Store) Jobs() ([]Job, error) {
	rows, err := s.db.Query(`SELECT `+jobColumns+` FROM jobs
		WHERE state IN (?, ?, ?, ?) OR id IN (SELECT id FROM jobs WHERE state IN (?, ?, ?) ORDER BY id DESC LIMIT 5)
		ORDER BY id DESC`, Queued, Running, Blocked, Manual, Done, Failed, Exhausted)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ClaimIssue records issue as handled by loop, and reports whether it was new.
func (s *Store) ClaimIssue(loop string, issue int) (bool, error) {
	res, err := s.db.Exec(`INSERT INTO issues (loop, number) VALUES (?, ?) ON CONFLICT DO NOTHING`, loop, issue)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UnclaimIssue forgets that loop handled issue, so the next poll runs it again.
func (s *Store) UnclaimIssue(loop string, issue int) error {
	_, err := s.db.Exec(`DELETE FROM issues WHERE loop = ? AND number = ?`, loop, issue)
	return err
}
