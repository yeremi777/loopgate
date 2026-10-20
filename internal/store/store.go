// Package store keeps loopd state in SQLite.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"

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
	ID       string
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

type Store struct {
	db *sql.DB
	// newID makes a candidate id for a new Job.
	newID func() string
}

// jobsTable is the columns of the jobs table; seq orders Jobs by creation.
const jobsTable = `(
	seq      INTEGER PRIMARY KEY AUTOINCREMENT,
	id       TEXT NOT NULL UNIQUE,
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
)`

const schema = `
CREATE TABLE IF NOT EXISTS loops (
	name   TEXT PRIMARY KEY,
	paused INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS jobs ` + jobsTable + `;
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
	if err := convertIntegerIDs(db); err != nil {
		return nil, errors.Join(fmt.Errorf("convert integer job ids: %w", err), db.Close())
	}
	return &Store{db: db, newID: newID}, nil
}

// integerColumns are the columns of a jobs table from before random ids, but its id.
const integerColumns = `loop, state, work, branch, worktree, base, issue, question, summary, pr_url, cost_usd, error`

// convertIntegerIDs gives each Job of a jobs table from before random ids a
// new id in one transaction, keeping its other fields and its creation
// order, and logs each new id. A converted table has a seq column and is left alone.
func convertIntegerIDs(db *sql.DB) (err error) {
	var converted bool
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'seq'`).Scan(&converted); err != nil || converted {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	rows, err := tx.Query(`SELECT id FROM jobs ORDER BY id`)
	if err != nil {
		return err
	}
	var old []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return errors.Join(err, rows.Close())
		}
		old = append(old, n)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE jobs_new ` + jobsTable); err != nil {
		return err
	}
	ids := make([]string, len(old))
	for i, n := range old {
		ids[i] = newID()
		if _, err := tx.Exec(`INSERT INTO jobs_new (seq, id, `+integerColumns+`) SELECT id, ?, `+integerColumns+` FROM jobs WHERE id = ?`, ids[i], n); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE jobs; ALTER TABLE jobs_new RENAME TO jobs`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for i, n := range old {
		log.Printf("job %d is now %s", n, ids[i])
	}
	return nil
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

// maxIDTries bounds how many stored ids CreateJob draws before it gives up.
const maxIDTries = 10

// CreateJob stores a queued Job under a new id that no stored Job has.
func (s *Store) CreateJob(loop string) (Job, error) {
	for range maxIDTries {
		id := s.newID()
		res, err := s.db.Exec(`INSERT INTO jobs (id, loop, state) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING`, id, loop, Queued)
		if err != nil {
			return Job{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return Job{}, err
		}
		if n == 1 {
			return Job{ID: id, Loop: loop, State: Queued}, nil
		}
	}
	return Job{}, fmt.Errorf("no unused job id after %d tries", maxIDTries)
}

// newID is 12 lowercase hex characters from crypto/rand.
func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) SetJobState(id string, state JobState) error {
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
func (s *Store) StartJob(id string, st Start) error {
	return s.updateJob(id, `UPDATE jobs SET state = ?, work = ?, branch = ?, worktree = ?, base = ?, issue = ? WHERE id = ?`,
		Running, st.Work, st.Branch, st.Worktree, st.Base, st.Issue, id)
}

func (s *Store) FinishJob(id string, f Finish) error {
	return s.updateJob(id, `UPDATE jobs SET state = ?, question = ?, summary = ?, pr_url = ?, cost_usd = cost_usd + ?, error = ? WHERE id = ?`,
		f.State, f.Question, f.Summary, f.PRURL, f.CostUSD, f.Error, id)
}

// InterruptRunning fails every queued or running Job, which a dead loopd left behind.
func (s *Store) InterruptRunning(reason string) error {
	_, err := s.db.Exec(`UPDATE jobs SET state = ?, error = ? WHERE state IN (?, ?)`, Failed, reason, Queued, Running)
	return err
}

func (s *Store) updateJob(id string, query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, fmt.Errorf("job %s: %w", id, ErrNotFound))
	}
	return nil
}

const jobColumns = `id, loop, state, work, branch, worktree, base, issue, question, summary, pr_url, cost_usd, error`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.Loop, &job.State, &job.Work, &job.Branch, &job.Worktree, &job.Base, &job.Issue, &job.Question, &job.Summary, &job.PRURL, &job.CostUSD, &job.Error)
	return job, err
}

func (s *Store) Job(id string) (Job, error) {
	job, err := scanJob(s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrNotFound)
	}
	return job, err
}

// Jobs are every Job in progress, that is queued, running, Blocked or
// manual, and the five newest that ended done, failed or exhausted, newest
// first by creation.
func (s *Store) Jobs() ([]Job, error) {
	rows, err := s.db.Query(`SELECT `+jobColumns+` FROM jobs
		WHERE state IN (?, ?, ?, ?) OR seq IN (SELECT seq FROM jobs WHERE state IN (?, ?, ?) ORDER BY seq DESC LIMIT 5)
		ORDER BY seq DESC`, Queued, Running, Blocked, Manual, Done, Failed, Exhausted)
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
