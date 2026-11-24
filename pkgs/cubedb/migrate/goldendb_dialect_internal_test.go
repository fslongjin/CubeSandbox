// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package migrate

import (
	"database/sql"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

func TestGoldenDBOverridesOnlyIncompatibleMigrations(t *testing.T) {
	mysql, mysqlFS, _, err := newProvider("mysql", &sql.DB{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gold, goldFS, _, err := newProvider("goldendb", &sql.DB{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mysqlSources, goldSources := mysql.ListSources(), gold.ListSources()
	if len(mysqlSources) != len(goldSources) {
		t.Fatalf("different migration counts: MySQL %d, GoldenDB %d", len(mysqlSources), len(goldSources))
	}
	for i, source := range mysqlSources {
		got := goldSources[i]
		want := source.Type
		switch source.Version {
		case 8, 9, 20260624113500, snapshotActiveVersion, 20260731120000, 20261112035937:
			want = goose.TypeGo
		}
		if got.Version != source.Version || got.Type != want {
			t.Fatalf("GoldenDB migration %d = version %d type %s, want %d %s", i, got.Version, got.Type, source.Version, want)
		}
	}
	original, err := fs.ReadFile(mysqlFS, snapshotActiveSQL)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintSource, err := fs.ReadFile(goldFS, snapshotActiveSQL)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(fingerprintSource) {
		t.Fatal("the canonical fingerprint source differs across dialects")
	}
}

// TestGoldenDBReusesTheMySQLMigrationSet pins the dialect alias. GoldenDB
// speaks the MySQL wire protocol and shares the schema, so it uses the same
// canonical SQL files and fingerprint store as MySQL. A copy of the migrations
// would let the two histories drift, and a missing entry would make
// dao.Migrate fail with "unknown dialect".
func TestGoldenDBReusesTheMySQLMigrationSet(t *testing.T) {
	mysqlSpec, ok := dialectSpecs["mysql"]
	if !ok {
		t.Fatal(`dialectSpecs has no "mysql" entry`)
	}
	goldenSpec, ok := dialectSpecs["goldendb"]
	if !ok {
		t.Fatal(`dialectSpecs has no "goldendb" entry; dao.Migrate would fail`)
	}

	if goldenSpec.dialect != database.DialectMySQL {
		t.Fatalf("goldendb dialect = %v, want DialectMySQL", goldenSpec.dialect)
	}
	if goldenSpec.subdir != mysqlSpec.subdir {
		t.Fatalf("goldendb subdir = %q, want %q (same migration directory)", goldenSpec.subdir, mysqlSpec.subdir)
	}

	mysqlFiles := listSQLFiles(t, mysqlSpec.rootFS, mysqlSpec.subdir)
	goldenFiles := listSQLFiles(t, goldenSpec.rootFS, goldenSpec.subdir)
	if len(mysqlFiles) == 0 {
		t.Fatal("mysql migration set is empty")
	}
	if !slices.Equal(goldenFiles, mysqlFiles) {
		t.Fatalf("goldendb migrations differ from mysql:\n goldendb: %v\n mysql:    %v", goldenFiles, mysqlFiles)
	}

	// Both dialects must share the fingerprint table owner, otherwise two
	// processes on different drivers would record incompatible fingerprints.
	if _, isMySQLStore := goldenSpec.store.(*mysqlFingerprintStore); !isMySQLStore {
		t.Fatalf("goldendb fingerprint store = %T, want *mysqlFingerprintStore", goldenSpec.store)
	}
}

// listSQLFiles returns the sorted .sql file names under subdir.
func listSQLFiles(t *testing.T, rootFS fs.FS, subdir string) []string {
	t.Helper()
	sub, err := fs.Sub(rootFS, subdir)
	if err != nil {
		t.Fatalf("fs.Sub(%q): %v", subdir, err)
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read dir %q: %v", subdir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}
