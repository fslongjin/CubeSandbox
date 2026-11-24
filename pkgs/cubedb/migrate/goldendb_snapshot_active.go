// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

const snapshotActiveSQL = "20260701040100_snapshot_runtime_active_binding.sql"
const snapshotActiveVersion = 20260701040100

// GoldenDB's proxy cannot evaluate the nested session variables in this migration.
// Keep the canonical SQL and version unchanged. Execute the same statements and
// metadata checks in order, evaluating the checks in Go before the backfill.
func goldenDBSnapshotActiveMigration(source fs.FS) (*goose.Migration, error) {
	raw, err := fs.ReadFile(source, snapshotActiveSQL)
	if err != nil {
		return nil, err
	}
	_, body, hasUp := strings.Cut(string(raw), "-- +goose Up\n")
	up, down, hasDown := strings.Cut(body, "-- +goose Down\n")
	before, checksAndTail, hasChecks := strings.Cut(up, "SET @snapshot_runtime_active_pk_ok")
	checks, _, hasCheckEnd := strings.Cut("SET @snapshot_runtime_active_pk_ok"+checksAndTail, "SET @snapshot_runtime_active_schema_ok")
	_, after, hasBackfill := strings.Cut(checksAndTail, "INSERT INTO `t_cube_snapshot_runtime_active`")
	if !hasUp || !hasDown || !hasChecks || !hasCheckEnd || !hasBackfill {
		return nil, fmt.Errorf("migrate: unexpected structure in %s", snapshotActiveSQL)
	}
	upStart := snapshotActiveStatements(before)
	upEnd := snapshotActiveStatements("INSERT INTO `t_cube_snapshot_runtime_active`" + after)
	downSteps := snapshotActiveStatements(down)
	metadataChecks := snapshotActiveStatements(checks)
	want := []int{2, 1, 1, 1}
	if len(upStart) != 8 || len(upEnd) != 2 || len(downSteps) != 3 || len(metadataChecks) != len(want) {
		return nil, fmt.Errorf("migrate: unexpected statement counts in %s", snapshotActiveSQL)
	}
	for i, statement := range metadataChecks {
		_, query, ok := strings.Cut(statement, ":= (")
		if !ok || !strings.HasSuffix(statement, ")") {
			return nil, fmt.Errorf("migrate: unexpected metadata check %d in %s", i+1, snapshotActiveSQL)
		}
		metadataChecks[i] = strings.TrimSpace(strings.TrimSuffix(query, ")"))
	}
	upFn := &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
		return withGoldenDBMigrationLock(ctx, db, upStart[0], upEnd[1], func(conn *sql.Conn) error {
			for _, stmt := range upStart[1:] {
				if _, err := conn.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			for i, query := range metadataChecks {
				var count int
				if err := conn.QueryRowContext(ctx, query).Scan(&count); err != nil {
					return fmt.Errorf("snapshot active index check %d: %w", i+1, err)
				}
				if count != want[i] {
					return fmt.Errorf("t_cube_snapshot_runtime_active schema mismatch: check %d returned %d, want %d", i+1, count, want[i])
				}
			}
			_, err := conn.ExecContext(ctx, upEnd[0])
			return err
		})
	}}
	downFn := &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
		return withGoldenDBMigrationLock(ctx, db, downSteps[0], downSteps[2], func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, downSteps[1])
			return err
		})
	}}
	return goose.NewGoMigration(snapshotActiveVersion, upFn, downFn), nil
}

// The specific embedded migration has newline-terminated statements. No
// general-purpose SQL parsing is needed for its fixed statement boundaries.
func snapshotActiveStatements(body string) []string {
	var statements []string
	for _, block := range strings.Split(body, ";\n") {
		block = strings.TrimSpace(block)
		for strings.HasPrefix(block, "--") {
			_, block, _ = strings.Cut(block, "\n")
			block = strings.TrimSpace(block)
		}
		if block != "" {
			statements = append(statements, block)
		}
	}
	return statements
}

func withGoldenDBMigrationLock(ctx context.Context, db *sql.DB, acquire, release string, run func(*sql.Conn) error) (result error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, acquire); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, release); err != nil {
			result = errors.Join(result, fmt.Errorf("release GoldenDB migration lock: %w", err))
		}
	}()
	return run(conn)
}
