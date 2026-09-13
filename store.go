package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the panel's entire persistent state: one SQLite file.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  email      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password   TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'user',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  ip         TEXT NOT NULL DEFAULT '',
  agent      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);
CREATE TABLE IF NOT EXISTS kernel_sessions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  slug       TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  ready_at   INTEGER,
  stopped_at INTEGER,
  seconds    INTEGER NOT NULL DEFAULT 0,
  reason     TEXT NOT NULL DEFAULT '',
  error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS kernel_sessions_started ON kernel_sessions(started_at);
CREATE TABLE IF NOT EXISTS runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  kind        TEXT NOT NULL,
  prompt      TEXT NOT NULL,
  params      TEXT NOT NULL DEFAULT '{}',
  status      TEXT NOT NULL,
  user_id     INTEGER NOT NULL DEFAULT 0,
  kernel_id   INTEGER NOT NULL DEFAULT 0,
  file        TEXT NOT NULL DEFAULT '',
  mime        TEXT NOT NULL DEFAULT '',
  bytes       INTEGER NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  finished_at INTEGER
);
CREATE INDEX IF NOT EXISTS runs_created ON runs(created_at DESC);
CREATE INDEX IF NOT EXISTS runs_kind ON runs(kind, created_at DESC);
`

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLite happy without a write lock of our own.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---------------------------------------------------------------- settings

func (s *Store) Get(key, def string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Store) GetInt(key string, def int) int {
	v, err := strconv.Atoi(s.Get(key, ""))
	if err != nil {
		return def
	}
	return v
}

func (s *Store) GetBool(key string, def bool) bool {
	switch s.Get(key, "") {
	case "1", "true":
		return true
	case "0", "false":
		return false
	}
	return def
}

func (s *Store) Set(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

func (s *Store) SetInt(key string, v int) error { return s.Set(key, strconv.Itoa(v)) }
func (s *Store) SetBool(key string, v bool) error {
	return s.Set(key, map[bool]string{true: "1", false: "0"}[v])
}

// ------------------------------------------------------------------- users

type User struct {
	ID       int64
	Email    string
	Password string
	Role     string
	Created  time.Time
}

var ErrNoUser = errors.New("no such user")

func (s *Store) CreateUser(email, hash, role string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO users (email, password, role, created_at) VALUES (?, ?, ?, ?)`,
		strings.TrimSpace(email), hash, role, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UserByEmail(email string) (*User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT id, email, password, role, created_at FROM users WHERE email = ?`, strings.TrimSpace(email)))
}

func (s *Store) UserByID(id int64) (*User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT id, email, password, role, created_at FROM users WHERE id = ?`, id))
}

func (s *Store) scanUser(row *sql.Row) (*User, error) {
	var u User
	var created int64
	if err := row.Scan(&u.ID, &u.Email, &u.Password, &u.Role, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoUser
		}
		return nil, err
	}
	u.Created = time.Unix(created, 0)
	return &u, nil
}

func (s *Store) CountUsers() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// ---------------------------------------------------------------- sessions

func (s *Store) CreateSession(token string, userID int64, ttl time.Duration, ip, agent string) error {
	now := time.Now()
	_, err := s.db.Exec(
		`INSERT INTO sessions (token, user_id, created_at, expires_at, ip, agent) VALUES (?, ?, ?, ?, ?, ?)`,
		token, userID, now.Unix(), now.Add(ttl).Unix(), ip, agent)
	return err
}

func (s *Store) SessionUser(token string) (*User, error) {
	var id int64
	var exp int64
	err := s.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token = ?`, token).Scan(&id, &exp)
	if err != nil {
		return nil, ErrNoUser
	}
	if time.Now().Unix() > exp {
		s.DeleteSession(token)
		return nil, ErrNoUser
	}
	return s.UserByID(id)
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) PurgeSessions() {
	s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
}

// -------------------------------------------------------- kernel bookkeeping

type KernelSession struct {
	ID      int64
	Slug    string
	Started time.Time
	Ready   *time.Time
	Stopped *time.Time
	Seconds int
	Reason  string
	Error   string
}

func (s *Store) StartKernelSession(slug string, at time.Time) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO kernel_sessions (slug, started_at) VALUES (?, ?)`, slug, at.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) MarkKernelReady(id int64, at time.Time) error {
	_, err := s.db.Exec(`UPDATE kernel_sessions SET ready_at = ? WHERE id = ? AND ready_at IS NULL`, at.Unix(), id)
	return err
}

// FinishKernelSession closes the books on a session. Billed seconds run from
// the request, not from ready: Kaggle's clock starts when the session is
// allocated, so the cold start is on us too.
func (s *Store) FinishKernelSession(id int64, at time.Time, reason, errMsg string) error {
	_, err := s.db.Exec(`
		UPDATE kernel_sessions
		   SET stopped_at = ?, reason = ?, error = ?, seconds = MAX(0, ? - started_at)
		 WHERE id = ? AND stopped_at IS NULL`,
		at.Unix(), reason, errMsg, at.Unix(), id)
	return err
}

// OpenKernelSession returns the session that was never closed, if any. The
// panel can be restarted while a kernel is still running on Kaggle.
func (s *Store) OpenKernelSession() (*KernelSession, error) {
	row := s.db.QueryRow(`
		SELECT id, slug, started_at, ready_at, stopped_at, seconds, reason, error
		  FROM kernel_sessions WHERE stopped_at IS NULL ORDER BY started_at DESC LIMIT 1`)
	return scanKernelSession(row)
}

func scanKernelSession(row *sql.Row) (*KernelSession, error) {
	var k KernelSession
	var started int64
	var ready, stopped sql.NullInt64
	if err := row.Scan(&k.ID, &k.Slug, &started, &ready, &stopped, &k.Seconds, &k.Reason, &k.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	k.Started = time.Unix(started, 0)
	if ready.Valid {
		t := time.Unix(ready.Int64, 0)
		k.Ready = &t
	}
	if stopped.Valid {
		t := time.Unix(stopped.Int64, 0)
		k.Stopped = &t
	}
	return &k, nil
}

// UsedSeconds totals billed kernel time since `since`. A session that is still
// open counts up to now, so the figure never lags behind reality.
func (s *Store) UsedSeconds(since time.Time, now time.Time) int {
	var closed, open sql.NullInt64
	s.db.QueryRow(`SELECT COALESCE(SUM(seconds), 0) FROM kernel_sessions
	                WHERE stopped_at IS NOT NULL AND stopped_at >= ?`, since.Unix()).Scan(&closed)
	s.db.QueryRow(`SELECT COALESCE(SUM(? - MAX(started_at, ?)), 0) FROM kernel_sessions
	                WHERE stopped_at IS NULL`, now.Unix(), since.Unix()).Scan(&open)
	total := int(closed.Int64 + open.Int64)
	if total < 0 {
		return 0
	}
	return total
}

// --------------------------------------------------------------------- runs

type Run struct {
	ID       int64      `json:"id"`
	Kind     string     `json:"kind"`
	Prompt   string     `json:"prompt"`
	Params   string     `json:"params"`
	Status   string     `json:"status"`
	File     string     `json:"file"`
	Mime     string     `json:"mime"`
	Bytes    int64      `json:"bytes"`
	Error    string     `json:"error,omitempty"`
	Created  time.Time  `json:"created"`
	Finished *time.Time `json:"finished,omitempty"`
}

func (s *Store) CreateRun(kind, prompt, params string, userID int64, kernelID int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO runs (kind, prompt, params, status, user_id, kernel_id, created_at) VALUES (?, ?, ?, 'running', ?, ?, ?)`,
		kind, prompt, params, userID, kernelID, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(id int64, file, mime string, size int64) error {
	_, err := s.db.Exec(
		`UPDATE runs SET status = 'done', file = ?, mime = ?, bytes = ?, finished_at = ? WHERE id = ?`,
		file, mime, size, time.Now().Unix(), id)
	return err
}

func (s *Store) FailRun(id int64, msg string) error {
	_, err := s.db.Exec(`UPDATE runs SET status = 'failed', error = ?, finished_at = ? WHERE id = ?`,
		msg, time.Now().Unix(), id)
	return err
}

func (s *Store) Runs(kind string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, kind, prompt, params, status, file, mime, bytes, error, created_at, finished_at FROM runs`
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Run{}
	for rows.Next() {
		var r Run
		var created int64
		var finished sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Kind, &r.Prompt, &r.Params, &r.Status, &r.File, &r.Mime,
			&r.Bytes, &r.Error, &created, &finished); err != nil {
			return nil, err
		}
		r.Created = time.Unix(created, 0)
		if finished.Valid {
			t := time.Unix(finished.Int64, 0)
			r.Finished = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Run(id int64) (*Run, error) {
	var r Run
	var created int64
	var finished sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, kind, prompt, params, status, file, mime, bytes, error, created_at, finished_at FROM runs WHERE id = ?`, id).
		Scan(&r.ID, &r.Kind, &r.Prompt, &r.Params, &r.Status, &r.File, &r.Mime, &r.Bytes, &r.Error, &created, &finished)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.Created = time.Unix(created, 0)
	if finished.Valid {
		t := time.Unix(finished.Int64, 0)
		r.Finished = &t
	}
	return &r, nil
}

// AbandonRuns marks runs that were in flight when the panel stopped. Without
// this they would sit at "running" for ever after a restart.
func (s *Store) AbandonRuns() {
	s.db.Exec(`UPDATE runs SET status = 'failed', error = 'the panel restarted while this run was in flight', finished_at = ?
	            WHERE status = 'running'`, time.Now().Unix())
}
