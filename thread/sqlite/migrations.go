package sqlite

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	msqlite "modernc.org/sqlite"
)

// The migrations are embedded SQL files applied in order on Open,
// tracked in schema_migrations — the store's shape (goose's, without
// the dependency), so a thread database and a run-record database age
// the same way. A file whose recorded version is ahead of this
// binary's highest fails Open with ErrNewerSchema: never run nothing
// and say nothing (LangGraph's four burned checkpoint formats, ADR
// 0010 §2.3, are the reason both modules refuse).

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrations maps version → file name; the list is derived from the
// embedded directory so a new .sql file is picked up by adding it.
var migrations = func() map[int]string {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		panic("sqlite: embedded migrations unreadable: " + err.Error())
	}
	out := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			panic("sqlite: migration " + name + " must start with its numeric version")
		}
		out[v] = name
	}
	return out
}()

func highestMigration() int {
	versions := make([]int, 0, len(migrations))
	for v := range migrations {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	if len(versions) == 0 {
		return 0
	}
	return versions[len(versions)-1]
}

// ErrNewerSchema is returned by Open when the file's schema_migrations
// is ahead of this binary's highest migration: a database written by a
// newer weft fails loudly instead of running nothing and saying
// nothing — the store's rule (its ErrNewerSchema), verbatim, because
// the failure is the same failure.
var ErrNewerSchema = errors.New("sqlite: database schema is newer than this weft")

// setWAL switches the database file to WAL — once, on Open, not per
// connection. The mode switch needs a brief exclusive lock that SQLite
// does not take the busy handler's patience for, so concurrent first
// Opens (two processes on a fresh file) can each see SQLITE_BUSY where
// every other statement would simply wait. The loop converges:
// whichever process wins the switch writes WAL into the file header,
// and every later attempt reads mode "wal" back and returns — so the
// retry budget only has to outlast the switch itself, and a BUSY that
// outlives the budget still fails Open loudly. (The store's setWAL,
// copied for the same reason it was written.)
func setWAL(db *sql.DB) error {
	for try := 0; ; try++ {
		var mode string
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
			return err
		}
		if strings.EqualFold(mode, "wal") {
			return nil
		}
		if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err == nil {
			return nil
		} else if !isBusy(err) || try >= 20 {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// isBusy reports whether err is SQLite's SQLITE_BUSY or SQLITE_LOCKED,
// including their extended codes (the low byte carries the primary).
func isBusy(err error) bool {
	var serr *msqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	switch serr.Code() & 0xff {
	case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
		return true
	}
	return false
}

// isConstraint reports whether err is SQLite's constraint violation
// (the primary-key family): Create maps it to ErrExists.
func isConstraint(err error) bool {
	var serr *msqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	// 19 SQLITE_CONSTRAINT, 2067 UNIQUE — the extended codes keep the
	// primary in the low byte.
	return serr.Code()&0xff == 19
}

// migrate applies pending migrations, each in its own transaction, and
// refuses a schema ahead of this binary.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var current sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	if current.Valid && int(current.Int64) > highestMigration() {
		return fmt.Errorf("%w: file has migration %d, this binary knows up to %d",
			ErrNewerSchema, current.Int64, highestMigration())
	}
	versions := make([]int, 0, len(migrations))
	for v := range migrations {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	for _, v := range versions {
		if current.Valid && int(current.Int64) >= v {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		// Re-read the applied version inside the transaction: another
		// process may have migrated this file between the read above
		// and the write lock this Begin took (two first Opens on a
		// fresh file). The loser skips what the winner applied instead
		// of failing on a table or primary key that now exists.
		var applied sql.NullInt64
		if err := tx.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&applied); err != nil {
			_ = tx.Rollback()
			return err
		}
		if applied.Valid && int(applied.Int64) >= v {
			_ = tx.Rollback()
			continue
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlite: migration %s: %w", migrations[v], err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			v, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
