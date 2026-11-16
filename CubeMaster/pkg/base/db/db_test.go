// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package db_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/db"
	"github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dao"
	_ "github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dao/driver/postgres"
)

func TestInitReturnsDaoDefaultOnPostgreSQL(t *testing.T) {
	env := newPostgresTestEnv(t)
	defer env.teardown()

	cfg := dao.Config{
		Driver: "postgres",
		Addr:   env.addr,
		User:   "cube",
		Pwd:    "cube_pass",
		DBName: "cube_test",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := dao.Open(ctx, cfg); err != nil {
		t.Fatalf("dao.Open: %v", err)
	}
	defer func() { _ = dao.Close() }()

	got := db.Init(&config.DBConfig{Driver: "postgres"})
	if got != dao.Default() {
		t.Fatal("db.Init must return the global dao handle opened by dao.Open")
	}
}

func TestConfigFromDBConfig(t *testing.T) {
	if _, err := db.ConfigFromDBConfig(nil); err == nil {
		t.Fatal("ConfigFromDBConfig(nil) must return an error")
	}

	src := &config.DBConfig{
		Driver:                      "postgres",
		Addr:                        "127.0.0.1:5432",
		User:                        "cube",
		Pwd:                         "cube_pass",
		DBName:                      "cube_test",
		ConnTimeout:                 1,
		ReadTimeout:                 2,
		WriteTimeout:                3,
		MaxIdleConns:                4,
		MaxOpenConns:                5,
		MaxConnLifeTimeSeconds:      6,
		MigrationLockTimeoutSeconds: 7,
	}
	got, err := db.ConfigFromDBConfig(src)
	if err != nil {
		t.Fatalf("ConfigFromDBConfig: %v", err)
	}
	want := dao.Config{
		Driver:                      "postgres",
		Addr:                        "127.0.0.1:5432",
		User:                        "cube",
		Pwd:                         "cube_pass",
		DBName:                      "cube_test",
		ConnTimeoutSeconds:          1,
		ReadTimeoutSeconds:          2,
		WriteTimeoutSeconds:         3,
		MaxIdleConns:                4,
		MaxOpenConns:                5,
		MaxConnLifeTimeSeconds:      6,
		MigrationLockTimeoutSeconds: 7,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigFromDBConfig mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

// TestConfigFromDBConfigMapsGoldenDB covers the engine block: a goldendb
// deployment keeps its CN list in Addr and its mode/proxy settings in Extra,
// which is what dao.Open's identity comparison and the driver both read.
func TestConfigFromDBConfigMapsGoldenDB(t *testing.T) {
	blacklist := int64(500)
	masterBlacklist := int64(-1)
	lower := false
	src := &config.DBConfig{
		Driver: "goldendb",
		Addr:   "10.0.0.1:3306,10.0.0.2:3306",
		User:   "cube",
		Pwd:    "cube_pass",
		DBName: "cube_mvp",
		GoldenDB: &config.GoldenDBConfig{
			Mode:                "master_cn",
			BlackListMS:         &blacklist,
			MasterBlackListMS:   &masterBlacklist,
			ProxyGroups:         []string{"10.0.1.1:3306,10.0.1.2:3306", "10.1.0.1:3306"},
			MinConnectionProxys: 1,
			ColumnsNameToLower:  &lower,
		},
	}
	got, err := db.ConfigFromDBConfig(src)
	if err != nil {
		t.Fatalf("ConfigFromDBConfig: %v", err)
	}
	if got.Driver != "goldendb" {
		t.Fatalf("Driver = %q", got.Driver)
	}
	if got.Addr != "10.0.0.1:3306,10.0.0.2:3306" {
		t.Fatalf("Addr = %q; the CN list must stay comma separated", got.Addr)
	}
	want := map[string]string{
		"mode":                  "master_cn",
		"blacklist_ms":          "500",
		"master_blacklist_ms":   "-1",
		"proxy_groups":          "10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306",
		"min_connection_proxys": "1",
		"columns_name_to_lower": "false",
	}
	if !reflect.DeepEqual(got.Extra, want) {
		t.Fatalf("Extra mismatch:\n got: %+v\nwant: %+v", got.Extra, want)
	}
}

// TestConfigFromDBConfigIgnoresGoldenDBBlockForOtherEngines keeps plain MySQL
// and PostgreSQL config identities byte-identical to what they were before the
// GoldenDB block existed.
func TestConfigFromDBConfigIgnoresGoldenDBBlockForOtherEngines(t *testing.T) {
	got, err := db.ConfigFromDBConfig(&config.DBConfig{
		Driver: "mysql",
		Addr:   "127.0.0.1:3306",
		User:   "cube",
		DBName: "cube_mvp",
	})
	if err != nil {
		t.Fatalf("ConfigFromDBConfig: %v", err)
	}
	if got.Extra != nil {
		t.Fatalf("Extra = %+v, want nil for a plain MySQL config", got.Extra)
	}
}
