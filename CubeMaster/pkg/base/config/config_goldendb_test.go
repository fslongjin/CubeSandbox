// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGoldenDBInstanceDBConfigRoundTrip locks the yaml shape the Helm chart and
// one-click both render: a comma-separated CN list in addr plus a nested
// goldendb block. A parse miss here would leave the driver on its defaults
// while the operator believes the mode was applied.
func TestGoldenDBInstanceDBConfigRoundTrip(t *testing.T) {
	const doc = `
instance_db_config:
  driver: "goldendb"
  addr: "10.0.0.30:3306,10.0.0.31:3306"
  user: "cube"
  pwd: "cube_pass"
  db_name: "cube_mvp"
  conn_timeout: 5
  goldendb:
    mode: "master_cn"
    proxy_groups:
      - "10.0.1.1:3306,10.0.1.2:3306"
      - "10.1.0.1:3306"
    min_connection_proxys: 1
    blacklist_ms: 500
    master_blacklist_ms: -1
    columns_name_to_lower: false
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	db := cfg.InstanceDBConfig
	if db == nil {
		t.Fatal("instance_db_config not parsed")
	}
	if db.Driver != "goldendb" {
		t.Fatalf("Driver = %q", db.Driver)
	}
	if db.Addr != "10.0.0.30:3306,10.0.0.31:3306" {
		t.Fatalf("Addr = %q; the CN list must stay intact", db.Addr)
	}
	if db.GoldenDB == nil {
		t.Fatal("goldendb block not parsed")
	}
	g := db.GoldenDB
	if g.Mode != "master_cn" {
		t.Fatalf("Mode = %q", g.Mode)
	}
	if len(g.ProxyGroups) != 2 ||
		g.ProxyGroups[0] != "10.0.1.1:3306,10.0.1.2:3306" ||
		g.ProxyGroups[1] != "10.1.0.1:3306" {
		t.Fatalf("ProxyGroups = %v", g.ProxyGroups)
	}
	if g.MinConnectionProxys != 1 {
		t.Fatalf("MinConnectionProxys = %d", g.MinConnectionProxys)
	}
	if g.BlackListMS == nil || *g.BlackListMS != 500 {
		t.Fatalf("BlackListMS = %v", g.BlackListMS)
	}
	if g.MasterBlackListMS == nil || *g.MasterBlackListMS != -1 {
		t.Fatalf("MasterBlackListMS = %v", g.MasterBlackListMS)
	}
	if g.ColumnsNameToLower == nil || *g.ColumnsNameToLower {
		t.Fatalf("ColumnsNameToLower = %v, want an explicit false", g.ColumnsNameToLower)
	}
}

// TestGoldenDBBlockIsOptional keeps plain MySQL and PostgreSQL configs working:
// the block must stay nil when the yaml does not mention it.
func TestGoldenDBBlockIsOptional(t *testing.T) {
	const doc = `
instance_db_config:
  driver: "mysql"
  addr: "127.0.0.1:3306"
  user: "cube"
  pwd: "cube_pass"
  db_name: "cube_mvp"
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if cfg.InstanceDBConfig == nil {
		t.Fatal("instance_db_config not parsed")
	}
	if cfg.InstanceDBConfig.GoldenDB != nil {
		t.Fatalf("GoldenDB = %+v, want nil for a plain MySQL config", cfg.InstanceDBConfig.GoldenDB)
	}
}
