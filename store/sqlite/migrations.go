package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The migrations are embedded SQL files applied in order on Open,
// tracked in schema_migrations — goose's shape without the dependency
// (Crush's approach, ADR 0010 §2.3). A file whose recorded version is
// ahead of this binary's highest fails Open with ErrNewerSchema:
// never run nothing and say nothing.

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
