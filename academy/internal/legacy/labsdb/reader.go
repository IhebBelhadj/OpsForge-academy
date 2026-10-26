// Package labsdb reads Phase 10's labs.db, a SQLite file (its schema: OpsForge's lab/migrations/0001_init.sql,
// plain SQL), for `academy migrate-legacy`; internal/legacy/postgres writes what it read.
package labsdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite" (pure Go, no cgo): Phase 10's file only

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/legacy"
)

// Reader implements legacy.Reader.
type Reader struct{}

var _ legacy.Reader = Reader{}

// Read reads every learner row of the Phase 10 database at path, opened read-only.
func (Reader) Read(ctx context.Context, path string) (legacy.Source, error) {
	if _, err := os.Stat(path); err != nil {
		return legacy.Source{}, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return legacy.Source{}, err
	}
	defer func() { _ = db.Close() }()
	var src legacy.Source
	if err := each(ctx, db, `SELECT id, lab_id, content_hash, started_at, finished_at, status, hints_used, seconds, xp
		FROM attempts ORDER BY id`, func(rows *sql.Rows) error {
		var a legacy.Attempt
		var started string
		var finished sql.NullString
		var seconds sql.NullFloat64
		if err := rows.Scan(&a.ID, &a.Lab, &a.Hash, &started, &finished, &a.Status, &a.HintsUsed, &seconds, &a.XP); err != nil {
			return err
		}
		var err error
		if a.StartedAt, err = phase10Time(started); err != nil {
			return err
		}
		if finished.Valid {
			t, err := phase10Time(finished.String)
			if err != nil {
				return err
			}
			a.FinishedAt = &t
		}
		if seconds.Valid {
			a.Seconds = &seconds.Float64
		}
		src.Attempts = append(src.Attempts, a)
		return nil
	}); err != nil {
		return legacy.Source{}, fmt.Errorf("attempts: %w", err)
	}
	if err := each(ctx, db, `SELECT attempt_id, step_id, passed, detail, at FROM step_results ORDER BY id`,
		func(rows *sql.Rows) error {
			var s legacy.StepResult
			var detail, at string
			if err := rows.Scan(&s.Attempt, &s.Step, &s.Passed, &detail, &at); err != nil {
				return err
			}
			s.Detail = []byte(detail)
			var err error
			s.At, err = phase10Time(at)
			src.Steps = append(src.Steps, s)
			return err
		}); err != nil {
		return legacy.Source{}, fmt.Errorf("step results: %w", err)
	}
	if err := each(ctx, db, `SELECT id, kind, COALESCE(subject, ''), data, at FROM events ORDER BY id`,
		func(rows *sql.Rows) error {
			var e legacy.Event
			var data, at string
			if err := rows.Scan(&e.ID, &e.Kind, &e.Subject, &data, &at); err != nil {
				return err
			}
			e.Data = []byte(data)
			var err error
			e.At, err = phase10Time(at)
			src.Events = append(src.Events, e)
			return err
		}); err != nil {
		return legacy.Source{}, fmt.Errorf("events: %w", err)
	}
	if err := each(ctx, db, `SELECT badge_id, awarded_at FROM user_badges ORDER BY awarded_at, badge_id`,
		func(rows *sql.Rows) error {
			var a legacy.Award
			var at string
			if err := rows.Scan(&a.Badge, &at); err != nil {
				return err
			}
			var err error
			a.At, err = phase10Time(at)
			src.Awards = append(src.Awards, a)
			return err
		}); err != nil {
		return legacy.Source{}, fmt.Errorf("awards: %w", err)
	}
	return src, nil
}

func each(ctx context.Context, db *sql.DB, query string, row func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := row(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// phase10Time reads Phase 10's timestamps (strftime '%Y-%m-%dT%H:%M:%SZ').
func phase10Time(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }
