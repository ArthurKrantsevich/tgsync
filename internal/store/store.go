// Package store persists node state in SQLite.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Session states. They are persisted and shown as topic emoji.
const (
	StateQueued      = "queued"
	StateStarting    = "starting"
	StateRunning     = "running"
	StateWaiting     = "waiting"
	StateIdle        = "idle"
	StateInterrupted = "interrupted"
	StateFailed      = "failed"
	StateClosed      = "closed"
)

//go:embed schema.sql
var schema string

// migrations upgrade databases created by older versions; each may run again.
var migrations = []string{
	`ALTER TABLE sessions ADD COLUMN profile TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN topic_deleted_at INTEGER NOT NULL DEFAULT 0`,
}

// Store is the node database.
type Store struct{ db *sql.DB }

// SessionRow is one agent session bound to a Telegram topic.
type SessionRow struct {
	ID              int64
	ThreadID        int
	Project         string
	Cwd             string
	Title           string
	ClaudeSessionID string
	State           string
	Mode            string
	Profile         string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Rule is a saved "always allow" permission for a project.
// Pattern is a command prefix for Bash and empty for other tools.
type Rule struct {
	Tool    string
	Pattern string
}

// Open opens or creates the database and applies the schema.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const controlKey = "control_thread_id"

// ControlTopic returns the control topic id, or 0 when none was created yet.
func (s *Store) ControlTopic(ctx context.Context) (int, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, controlKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// SetControlTopic stores the control topic id.
func (s *Store) SetControlTopic(ctx context.Context, threadID int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		controlKey, strconv.Itoa(threadID))
	return err
}

const approveKey = "approve_mode"

// ApproveMode returns the stored approve mode, "" when unset.
func (s *Store) ApproveMode(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, approveKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetApproveMode stores the approve mode.
func (s *Store) SetApproveMode(ctx context.Context, mode string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		approveKey, mode)
	return err
}

// CreateSession inserts r and fills its ID and timestamps.
func (s *Store) CreateSession(ctx context.Context, r *SessionRow) error {
	now := time.Now()
	r.CreatedAt, r.UpdatedAt = now, now
	if r.Mode == "" {
		r.Mode = "default"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(thread_id, project, cwd, title, claude_session_id, state, mode, profile, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ThreadID, r.Project, r.Cwd, r.Title, r.ClaudeSessionID, r.State, r.Mode, r.Profile, now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// UpdateSession saves the mutable fields of r.
func (s *Store) UpdateSession(ctx context.Context, r *SessionRow) error {
	r.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET title = ?, claude_session_id = ?, state = ?, mode = ?, updated_at = ? WHERE id = ?`,
		r.Title, r.ClaudeSessionID, r.State, r.Mode, r.UpdatedAt.Unix(), r.ID)
	return err
}

const sessionCols = `id, thread_id, project, cwd, title, claude_session_id, state, mode, profile, created_at, updated_at`

func scanSessions(rows *sql.Rows) ([]SessionRow, error) {
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		var created, updated int64
		if err := rows.Scan(&r.ID, &r.ThreadID, &r.Project, &r.Cwd, &r.Title, &r.ClaudeSessionID,
			&r.State, &r.Mode, &r.Profile, &created, &updated); err != nil {
			return nil, err
		}
		r.CreatedAt, r.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenSessions returns every session that is not closed, oldest first.
func (s *Store) OpenSessions(ctx context.Context) ([]SessionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE state != ? ORDER BY id`, StateClosed)
	if err != nil {
		return nil, err
	}
	return scanSessions(rows)
}

// Cleanable returns sessions whose topics the 🧹 cleanup may delete: closed
// before `before`, or failed without a single agent turn. Oldest first.
func (s *Store) Cleanable(ctx context.Context, before time.Time) ([]SessionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions s WHERE topic_deleted_at = 0 AND (
		(state = ? AND updated_at < ?) OR
		(state = ? AND NOT EXISTS (SELECT 1 FROM usage u WHERE u.thread_id = s.thread_id)))
		ORDER BY updated_at, id`, StateClosed, before.Unix(), StateFailed)
	if err != nil {
		return nil, err
	}
	return scanSessions(rows)
}

// MarkTopicDeleted records that the session's topic no longer exists.
func (s *Store) MarkTopicDeleted(ctx context.Context, thread int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET topic_deleted_at = ? WHERE thread_id = ?`, time.Now().Unix(), thread)
	return err
}

// SessionCounts returns open sessions and closed sessions whose topic still exists.
func (s *Store) SessionCounts(ctx context.Context) (open, closed int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(state != ?), 0),
		COALESCE(SUM(state = ? AND topic_deleted_at = 0), 0) FROM sessions`, StateClosed, StateClosed).Scan(&open, &closed)
	return open, closed, err
}

// Get returns a kv value, "" when the key is missing.
func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Set stores a kv value.
func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AddRule saves an "always allow" rule; saving the same rule twice is a no-op.
func (s *Store) AddRule(ctx context.Context, project string, r Rule) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO permission_rules(project, tool, pattern, created_at) VALUES(?, ?, ?, ?)`,
		project, r.Tool, r.Pattern, time.Now().Unix())
	return err
}

// Rules returns the saved rules of a project.
func (s *Store) Rules(ctx context.Context, project string) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tool, pattern FROM permission_rules WHERE project = ? ORDER BY created_at`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.Tool, &r.Pattern); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UsageRow is what one model used in one turn.
type UsageRow struct {
	At          time.Time
	ThreadID    int
	Project     string
	Model       string
	Input       int
	Output      int
	CacheRead   int
	CacheCreate int
	CostUSD     float64
}

// UsageTotal sums usage rows. Tokens counts input, output and cache
// creation; cache reads are cheap and kept apart.
type UsageTotal struct {
	Project   string
	Turns     int
	Tokens    int
	CacheRead int
	CostUSD   float64
}

// AddUsage records one usage row.
func (s *Store) AddUsage(ctx context.Context, r UsageRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO usage(at, thread_id, project, model, input, output, cache_read, cache_create, cost)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.At.UnixMilli(), r.ThreadID, r.Project, r.Model,
		r.Input, r.Output, r.CacheRead, r.CacheCreate, r.CostUSD)
	return err
}

const usageSums = `COUNT(DISTINCT at), COALESCE(SUM(input + output + cache_create), 0), COALESCE(SUM(cache_read), 0), COALESCE(SUM(cost), 0)`

// UsageSince sums usage by project since a time, most tokens first.
func (s *Store) UsageSince(ctx context.Context, since time.Time) ([]UsageTotal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, `+usageSums+` FROM usage WHERE at >= ?
		GROUP BY project ORDER BY 3 DESC, project`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageTotal
	for rows.Next() {
		var u UsageTotal
		if err := rows.Scan(&u.Project, &u.Turns, &u.Tokens, &u.CacheRead, &u.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SessionUsage sums the usage of one session.
func (s *Store) SessionUsage(ctx context.Context, thread int) (UsageTotal, error) {
	var u UsageTotal
	err := s.db.QueryRowContext(ctx, `SELECT `+usageSums+` FROM usage WHERE thread_id = ?`, thread).
		Scan(&u.Turns, &u.Tokens, &u.CacheRead, &u.CostUSD)
	return u, err
}

// RateLimitRow is the last known state of a subscription limit window.
type RateLimitRow struct {
	Name        string
	Status      string
	Utilization float64
	ResetsAt    time.Time
	UpdatedAt   time.Time
}

// SaveRateLimit stores the state of a window, replacing the previous one.
func (s *Store) SaveRateLimit(ctx context.Context, r RateLimitRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO rate_limits(name, status, utilization, resets_at, updated_at)
		VALUES(?, ?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET status = excluded.status,
		utilization = excluded.utilization, resets_at = excluded.resets_at, updated_at = excluded.updated_at`,
		r.Name, r.Status, r.Utilization, r.ResetsAt.Unix(), r.UpdatedAt.Unix())
	return err
}

// RateLimits returns the stored windows by name.
func (s *Store) RateLimits(ctx context.Context) ([]RateLimitRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, status, utilization, resets_at, updated_at FROM rate_limits ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RateLimitRow
	for rows.Next() {
		var r RateLimitRow
		var resets, updated int64
		if err := rows.Scan(&r.Name, &r.Status, &r.Utilization, &resets, &updated); err != nil {
			return nil, err
		}
		r.ResetsAt, r.UpdatedAt = time.Unix(resets, 0), time.Unix(updated, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}
