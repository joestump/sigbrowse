package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrate brings the database forward from whatever schema version it is on
// today to schemaVersion, applying each pending migration in order.
//
// Each migration runs inside its own transaction; foreign keys are toggled OFF
// for the duration of the migration (SQLite's recommended pattern when an
// ALTER may rebuild a referenced table) and back ON afterward, before commit
// recreates the regular constraint regime. Setting `PRAGMA user_version` is
// part of the same transaction, so a crashed apply leaves the version
// unchanged and the next Open retries from the same point.
//
// For a fresh database (version 0), every migration runs in order — there is
// no "current full schema" shortcut, so reasoning about how any database
// reached its current state is uniform.
func (s *Store) migrate(ctx context.Context) error {
	var current int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("database is at schema version %d, newer than this binary supports (%d)", current, schemaVersion)
	}
	for v := current + 1; v <= schemaVersion; v++ {
		if err := s.applyMigration(ctx, v, migrations[v]); err != nil {
			return fmt.Errorf("migration v%d: %w", v, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, version int, sqlText string) error {
	if sqlText == "" {
		return fmt.Errorf("no migration registered for version %d", version)
	}
	// Foreign keys are a connection-scoped pragma and cannot be toggled inside
	// a transaction. Toggle outside the tx, on the same connection — we use a
	// dedicated Conn so concurrent readers in the pool aren't affected.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	// Always restore foreign keys, even on failure.
	defer func() {
		_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		return fmt.Errorf("apply migration body: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	rollback = false
	return nil
}

// readUserVersion returns the database's current schema version. Useful for
// tests that need to assert the migration ran.
func readUserVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}
