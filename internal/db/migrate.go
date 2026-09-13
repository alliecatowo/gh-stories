package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/alliecatowo/gh-stories/infra/migrations"

	"github.com/jackc/pgx/v5"
)

type Migration struct {
	Version  int
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

// LoadMigrations parses NNNN_name.up.sql / .down.sql pairs.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	byVersion := map[int]*Migration{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		var direction string
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			direction = "up"
		case strings.HasSuffix(name, ".down.sql"):
			direction = "down"
		default:
			return nil, fmt.Errorf("migration %q must end in .up.sql or .down.sql", name)
		}
		base := strings.TrimSuffix(name, "."+direction+".sql")
		idx := strings.Index(base, "_")
		if idx <= 0 {
			return nil, fmt.Errorf("migration %q must be named NNNN_name", name)
		}
		version, err := strconv.Atoi(base[:idx])
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version", name)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		m := byVersion[version]
		if m == nil {
			m = &Migration{Version: version, Name: base[idx+1:]}
			byVersion[version] = m
		}
		if direction == "up" {
			m.UpSQL = string(body)
			sum := sha256.Sum256(body)
			m.Checksum = hex.EncodeToString(sum[:])
		} else {
			m.DownSQL = string(body)
		}
	}
	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.UpSQL == "" {
			return nil, fmt.Errorf("migration %d (%s) has no up file", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

const migrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	checksum   TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Migrate applies every pending migration, each in its own transaction, and
// refuses to proceed if an already-applied migration's contents changed.
func Migrate(ctx context.Context, pool *Pool, migrations []Migration) ([]int, error) {
	if _, err := pool.Exec(ctx, migrationsTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	applied := map[int]string{}
	rows, err := pool.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var ran []int
	for _, m := range migrations {
		if sum, ok := applied[m.Version]; ok {
			if sum != m.Checksum {
				return ran, fmt.Errorf(
					"migration %04d_%s was already applied but its contents changed "+
						"(recorded %s, found %s); migrations are immutable history",
					m.Version, m.Name, sum[:12], m.Checksum[:12])
			}
			continue
		}
		err := InTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
				return fmt.Errorf("apply %04d_%s: %w", m.Version, m.Name, err)
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1,$2,$3)`,
				m.Version, m.Name, m.Checksum)
			return err
		})
		if err != nil {
			return ran, err
		}
		ran = append(ran, m.Version)
	}
	return ran, nil
}

// MigrateEmbedded applies the migrations shipped inside the binary.
func MigrateEmbedded(ctx context.Context, pool *Pool) ([]int, error) {
	ms, err := LoadMigrations(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	return Migrate(ctx, pool, ms)
}
