// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/pressly/goose/v3"
)

func goldenDBMigrationOverrides(source fs.FS) ([]string, []*goose.Migration, error) {
	snapshot, err := goldenDBSnapshotActiveMigration(source)
	if err != nil {
		return nil, nil, err
	}
	names := []string{snapshotActiveSQL}
	migrations := []*goose.Migration{snapshot}
	for _, spec := range []struct {
		name     string
		up, down int
	}{
		{"0008_agenthub_template_persistence_mode.sql", 2, 2},
		{"0009_agenthub_openclaw_persistence_fields.sql", 9, 9},
		{"20260624113500_agenthub_template_source_snapshot_index.sql", 1, 1},
		{"20260731120000_soft_delete_purge_indexes.sql", 5, 5},
		{"20261112035937_convert_legacy_utf8mb4.sql", 1, 0},
	} {
		migration, err := goldenDBConditionalMigration(source, spec.name, spec.up, spec.down)
		if err != nil {
			return nil, nil, fmt.Errorf("migrate: GoldenDB override %s: %w", spec.name, err)
		}
		names = append(names, spec.name)
		migrations = append(migrations, migration)
	}
	return names, migrations, nil
}

var (
	// These patterns cover only the fixed COUNT/IF/PREPARE blocks in the listed migrations.
	countAssignment = regexp.MustCompile(`(?s)^SET (@\w+) := \(\s*(SELECT COUNT\(.*)\s*\)$`)
	conditionalSQL  = regexp.MustCompile(`(?s)^SET (@\w+) := IF\(\s*(?:(@\w+)|\((SELECT COUNT\(.*)\))\s*(=|>)\s*0\s*,\s*'((?:[^']|'')*)'\s*,\s*'SELECT 1'\s*\)$`)
)

type goldenDBMigrationStep struct {
	query     string
	statement string
	whenEmpty bool
}

func goldenDBConditionalMigration(source fs.FS, name string, upChecks, downChecks int) (*goose.Migration, error) {
	raw, err := fs.ReadFile(source, name)
	if err != nil {
		return nil, err
	}
	version, err := goose.NumericComponent(name)
	if err != nil {
		return nil, err
	}
	_, body, hasUp := strings.Cut(string(raw), "-- +goose Up\n")
	up, down, hasDown := strings.Cut(body, "-- +goose Down\n")
	if !hasUp || !hasDown {
		return nil, errors.New("missing migration direction")
	}
	upFn, err := goldenDBConditionalDirection(up, upChecks)
	if err != nil {
		return nil, fmt.Errorf("Up: %w", err)
	}
	downFn, err := goldenDBConditionalDirection(down, downChecks)
	if err != nil {
		return nil, fmt.Errorf("Down: %w", err)
	}
	return goose.NewGoMigration(version, upFn, downFn), nil
}

func goldenDBConditionalDirection(body string, wantChecks int) (*goose.GoFunc, error) {
	statements, err := goldenDBConditionalStatements(body)
	if err != nil {
		return nil, err
	}
	if len(statements) < 2 ||
		!strings.HasPrefix(statements[0], "CALL cubemaster_acquire_migration_lock(") ||
		!strings.HasPrefix(statements[len(statements)-1], "SELECT RELEASE_LOCK(") {
		return nil, errors.New("expected migration lock statements")
	}
	steps := make([]goldenDBMigrationStep, 0, len(statements)-2)
	checks := 0
	pending := statements[1 : len(statements)-1]
	for len(pending) > 0 {
		statement := pending[0]
		pending = pending[1:]
		if !strings.HasPrefix(statement, "SET @") {
			steps = append(steps, goldenDBMigrationStep{statement: statement})
			continue
		}

		count := countAssignment.FindStringSubmatch(statement)
		if count != nil && len(pending) > 0 {
			statement, pending = pending[0], pending[1:]
		}
		conditional := conditionalSQL.FindStringSubmatch(statement)
		if conditional == nil || len(pending) < 3 || !strings.HasPrefix(pending[0], "PREPARE ") {
			return nil, errors.New("unexpected conditional SQL block")
		}
		prepared, variable, ok := strings.Cut(strings.TrimPrefix(pending[0], "PREPARE "), " FROM ")
		if !ok || variable != conditional[1] || pending[1] != "EXECUTE "+prepared || pending[2] != "DEALLOCATE PREPARE "+prepared {
			return nil, errors.New("inconsistent prepared statement")
		}
		query := conditional[3]
		if count != nil && conditional[2] == count[1] {
			query = count[2]
		}
		if query == "" {
			return nil, errors.New("missing count query")
		}
		action := strings.ReplaceAll(conditional[5], "''", "'")
		steps = append(steps, goldenDBMigrationStep{strings.TrimSpace(query), action, conditional[4] == "="})
		checks++
		pending = pending[3:]
	}
	if checks != wantChecks {
		return nil, fmt.Errorf("got %d conditional checks, want %d", checks, wantChecks)
	}
	return &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
		return withGoldenDBMigrationLock(ctx, db, statements[0], statements[len(statements)-1], func(conn *sql.Conn) error {
			for _, step := range steps {
				if step.query != "" {
					var count int
					if err := conn.QueryRowContext(ctx, step.query).Scan(&count); err != nil {
						return fmt.Errorf("conditional migration count: %w", err)
					}
					if (count == 0) != step.whenEmpty {
						continue
					}
					const signal = "SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = "
					if message, ok := strings.CutPrefix(step.statement, signal); ok {
						return errors.New(strings.Trim(message, "'"))
					}
				}
				if _, err := conn.ExecContext(ctx, step.statement); err != nil {
					return err
				}
			}
			return nil
		})
	}}, nil
}

// Preserve the routine bodies in the charset migration as single statements.
func goldenDBConditionalStatements(body string) ([]string, error) {
	const begin = "-- +goose StatementBegin\n"
	const end = "-- +goose StatementEnd\n"
	var statements []string
	for {
		before, rest, found := strings.Cut(body, begin)
		statements = append(statements, snapshotActiveStatements(before)...)
		if !found {
			return statements, nil
		}
		routine, remaining, found := strings.Cut(rest, end)
		if !found {
			return nil, errors.New("missing StatementEnd")
		}
		statements = append(statements, strings.TrimSpace(routine))
		body = remaining
	}
}
