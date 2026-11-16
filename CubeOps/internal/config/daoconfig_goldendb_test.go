// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestGoldenDBConfigSelectsDriver pins the reason CubeOps has its own GoldenDB
// block instead of reusing DATABASE_URL: the driver needs a comma-separated CN
// list, which the URL parser rejects because it only accepts one host.
func TestGoldenDBConfigSelectsDriver(t *testing.T) {
	cfg := &Config{
		GoldenDB: GoldenDBConfig{
			Addrs:    "10.0.0.1:3306,10.0.0.2:3306",
			User:     "cube",
			Password: "cube_pass",
			DB:       "cube_mvp",
			Mode:     "masterslave",
		},
	}

	dc := mustDaoConfig(t, cfg)

	if dc.Driver != "goldendb" {
		t.Fatalf("Driver = %q, want goldendb", dc.Driver)
	}
	if dc.Addr != "10.0.0.1:3306,10.0.0.2:3306" {
		t.Fatalf("Addr = %q, want the CN list unchanged", dc.Addr)
	}
	if dc.User != "cube" || dc.Pwd != "cube_pass" || dc.DBName != "cube_mvp" {
		t.Fatalf("identity = %q/%q/%q", dc.User, dc.Pwd, dc.DBName)
	}
	if got := dc.Extra["mode"]; got != "masterslave" {
		t.Fatalf("Extra[mode] = %q", got)
	}
}

// TestGoldenDBConfigWinsOverDatabaseURL documents the precedence: a GoldenDB
// endpoint cannot be expressed as a URL, so a leftover DATABASE_URL must not
// silently pull the process back onto single-host MySQL.
func TestGoldenDBConfigWinsOverDatabaseURL(t *testing.T) {
	cfg := &Config{
		DatabaseURL: "mysql://cube:pass@127.0.0.1:3306/cube_mvp",
		MySQLHost:   "127.0.0.1",
		MySQLUser:   "cube",
		MySQLDB:     "cube_mvp",
		GoldenDB: GoldenDBConfig{
			Addrs: "10.0.0.1:3306",
			User:  "cube",
			DB:    "cube_mvp",
		},
	}

	dc := mustDaoConfig(t, cfg)

	if dc.Driver != "goldendb" {
		t.Fatalf("Driver = %q, want goldendb to win over DATABASE_URL", dc.Driver)
	}
}

func TestGoldenDBConfigValidatesRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		gdb  GoldenDBConfig
		want string
	}{
		{
			name: "missing user",
			gdb:  GoldenDBConfig{Addrs: "10.0.0.1:3306", DB: "cube_mvp"},
			want: "goldendb.user is required",
		},
		{
			name: "missing database",
			gdb:  GoldenDBConfig{Addrs: "10.0.0.1:3306", User: "cube"},
			want: "goldendb.db is required",
		},
		{
			name: "empty address in the list",
			gdb:  GoldenDBConfig{Addrs: "10.0.0.1:3306,,10.0.0.2:3306", User: "cube", DB: "cube_mvp"},
			want: "empty address",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{GoldenDB: tt.gdb}
			_, err := cfg.DaoConfig()
			if err == nil {
				t.Fatal("DaoConfig succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestGoldenDBExtraIncludesAllOptions(t *testing.T) {
	blacklist := int64(500)
	masterBlacklist := int64(-1)
	lower := false
	cfg := &Config{
		GoldenDB: GoldenDBConfig{
			Addrs:               "10.0.0.1:3306",
			User:                "cube",
			DB:                  "cube_mvp",
			Mode:                "master_cn",
			BlackListMS:         &blacklist,
			MasterBlackListMS:   &masterBlacklist,
			ProxyGroups:         []string{"10.0.1.1:3306,10.0.1.2:3306"},
			MinConnectionProxys: 2,
			ColumnsNameToLower:  &lower,
		},
	}

	dc := mustDaoConfig(t, cfg)

	want := map[string]string{
		"mode":                  "master_cn",
		"blacklist_ms":          "500",
		"master_blacklist_ms":   "-1",
		"proxy_groups":          "10.0.1.1:3306,10.0.1.2:3306",
		"min_connection_proxys": "2",
		"columns_name_to_lower": "false",
	}
	if !reflect.DeepEqual(dc.Extra, want) {
		t.Fatalf("Extra mismatch:\n got: %+v\nwant: %+v", dc.Extra, want)
	}
}

func TestGoldenDBEnvOverrides(t *testing.T) {
	t.Setenv("CUBE_GOLDENDB_ADDRS", "10.0.0.1:3306,10.0.0.2:3306")
	t.Setenv("CUBE_GOLDENDB_USER", "cube")
	t.Setenv("CUBE_GOLDENDB_PASSWORD", "cube_pass")
	t.Setenv("CUBE_GOLDENDB_DB", "cube_mvp")
	t.Setenv("CUBE_GOLDENDB_MODE", "random")
	t.Setenv("CUBE_GOLDENDB_PROXY_GROUPS", "10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306")
	t.Setenv("CUBE_GOLDENDB_MIN_CONNECTION_PROXYS", "1")

	cfg := &Config{}
	overrideFromEnv(cfg)

	if !cfg.GoldenDB.Configured() {
		t.Fatalf("GoldenDB not configured from env: %+v", cfg.GoldenDB)
	}
	wantGroups := []string{"10.0.1.1:3306,10.0.1.2:3306", "10.1.0.1:3306"}
	if !reflect.DeepEqual(cfg.GoldenDB.ProxyGroups, wantGroups) {
		t.Fatalf("ProxyGroups = %v, want %v", cfg.GoldenDB.ProxyGroups, wantGroups)
	}
	dc := mustDaoConfig(t, cfg)
	if got := dc.Extra["proxy_groups"]; got != "10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306" {
		t.Fatalf("Extra[proxy_groups] = %q", got)
	}
	if got := dc.Extra["mode"]; got != "random" {
		t.Fatalf("Extra[mode] = %q", got)
	}
}
