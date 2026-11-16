// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package goldendb

import (
	"strings"
	"testing"
	"time"

	"github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dao"
	gdbmysql "github.com/tencentcloud/CubeSandbox/pkgs/third_party/goldendb-go-sql-driver"
)

func baseConfig() dao.Config {
	return dao.Config{
		Driver:              DriverName,
		Addr:                "10.0.0.1:3306",
		User:                "cube",
		Pwd:                 "cube_pass",
		DBName:              "cube_mvp",
		Extra:               map[string]string{},
		ConnTimeoutSeconds:  3,
		ReadTimeoutSeconds:  7,
		WriteTimeoutSeconds: 9,
	}
}

// TestBuildDSNMasterSlaveDefault pins the control-plane default: master/slave
// mode, upstream-style timeouts, utf8mb4 and parseTime, and no leftover
// "loadbalance=true" (which would make migration locks land on a random CN).
func TestBuildDSNMasterSlaveDefault(t *testing.T) {
	cfg := baseConfig()
	cfg.Addr = "10.0.0.1:3306,10.0.0.2:3306"

	dsn, err := buildDSN(cfg)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	parsed, err := gdbmysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	if parsed.Addr != "10.0.0.1:3306,10.0.0.2:3306" {
		t.Fatalf("Addr = %q, want the comma-separated CN list", parsed.Addr)
	}
	if len(parsed.Addrs) != 2 {
		t.Fatalf("Addrs = %v, want two CNs", parsed.Addrs)
	}
	if parsed.LoadBalanceMode != ModeMasterSlave {
		t.Fatalf("LoadBalanceMode = %q, want %q", parsed.LoadBalanceMode, ModeMasterSlave)
	}
	if !parsed.LoadBalance {
		t.Fatal("LoadBalance = false; masterslave mode must enable balancing")
	}
	if parsed.User != "cube" || parsed.Passwd != "cube_pass" || parsed.DBName != "cube_mvp" {
		t.Fatalf("identity = %q/%q/%q", parsed.User, parsed.Passwd, parsed.DBName)
	}
	if parsed.Timeout != 3*time.Second || parsed.ReadTimeout != 7*time.Second || parsed.WriteTimeout != 9*time.Second {
		t.Fatalf("timeouts = %v/%v/%v", parsed.Timeout, parsed.ReadTimeout, parsed.WriteTimeout)
	}
	if !parsed.ParseTime {
		t.Fatal("ParseTime = false")
	}
	if !parsed.ColumnsNameToLower {
		t.Fatal("ColumnsNameToLower = false; default must be true")
	}
	if got := parsed.Params["charset"]; got != "utf8mb4" {
		t.Fatalf("charset = %q, want utf8mb4", got)
	}
	if strings.Contains(dsn, "masterConnection") {
		t.Fatalf("masterslave DSN must not set masterConnection: %q", dsn)
	}
}

func TestBuildDSNRandomMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Addr = "10.0.0.1:3306,10.0.0.2:3306"
	cfg.Extra[ExtraMode] = ModeRandom

	dsn, err := buildDSN(cfg)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	parsed, err := gdbmysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	if !parsed.LoadBalance || parsed.LoadBalanceMode != "" {
		t.Fatalf("random mode must be loadbalance only, got loadbalance=%v mode=%q",
			parsed.LoadBalance, parsed.LoadBalanceMode)
	}
}

// TestBuildDSNMasterCN checks masterConnection survives composition. The
// fork's FormatDSN cannot emit it, so buildDSN appends it as raw query text.
func TestBuildDSNMasterCN(t *testing.T) {
	cfg := baseConfig()
	cfg.Addr = "10.0.0.1:3306,10.0.0.2:3306"
	cfg.Extra[ExtraMode] = ModeMasterCN

	dsn, err := buildDSN(cfg)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if !strings.Contains(dsn, "masterConnection=true") {
		t.Fatalf("DSN lost masterConnection: %q", dsn)
	}
	// Parsing must succeed, which proves the address list and ports are valid
	// for master CN mode and that loadbalance was not set alongside it.
	if _, err := gdbmysql.ParseDSN(dsn); err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	if strings.Contains(dsn, "loadbalance=") {
		t.Fatalf("master CN DSN must not enable load balancing: %q", dsn)
	}
}

func TestBuildDSNSingleMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Extra[ExtraMode] = ModeSingle

	dsn, err := buildDSN(cfg)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if strings.Contains(dsn, "loadbalance") || strings.Contains(dsn, "masterConnection") {
		t.Fatalf("single mode must not set GoldenDB connection modes: %q", dsn)
	}
}

// TestBuildDSNProxyGroupsKeepsCommas guards the reason buildDSN writes these
// parameters verbatim: the fork reads proxygroupN without unescaping, so a
// QueryEscape round trip would turn the member separator into %2C.
func TestBuildDSNProxyGroupsKeepsCommas(t *testing.T) {
	cfg := baseConfig()
	cfg.Addr = "10.0.0.1:3306"
	cfg.Extra[ExtraMode] = ModeMasterCN
	cfg.Extra[ExtraProxyGroups] = "10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306"
	cfg.Extra[ExtraMinConnectionProxys] = "1"
	cfg.Extra[ExtraBlackListMS] = "500"
	cfg.Extra[ExtraMasterBlackListMS] = "-1"

	dsn, err := buildDSN(cfg)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	for _, want := range []string{
		"proxygroups=2",
		"minconnectionproxys=1",
		"proxygroup1=10.0.1.1:3306,10.0.1.2:3306",
		"proxygroup2=10.1.0.1:3306",
		"blacklist=500",
		"masterblacklist=-1",
	} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("DSN %q is missing %q", dsn, want)
		}
	}
	if strings.Contains(dsn, "%2C") {
		t.Fatalf("proxy group commas were escaped: %q", dsn)
	}
	parsed, err := gdbmysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	if got := parsed.ProxyGroup["proxygroup1"]; got != "10.0.1.1:3306,10.0.1.2:3306" {
		t.Fatalf("proxygroup1 = %q", got)
	}
	if parsed.BlackList != 500 || parsed.MasterBlackList != -1 {
		t.Fatalf("blacklists = %d/%d", parsed.BlackList, parsed.MasterBlackList)
	}
	if parsed.MinConnectionProxy != 1 || parsed.ProxyCount != 2 {
		t.Fatalf("proxy counts = %d/%d", parsed.MinConnectionProxy, parsed.ProxyCount)
	}
}

// TestBuildDSNCredentialsRoundTrip guards the reason buildDSN delegates the
// identity portion to the fork's FormatDSN: GoldenDB passwords legitimately
// contain ':' and '/' (the vendor's own example is "data2s@Loader"), so a
// hand-rolled "user:pass@addr" string would truncate them.
func TestBuildDSNCredentialsRoundTrip(t *testing.T) {
	for _, pwd := range []string{"simple", "p@ss:word/x", "p#a?b%c", "with space"} {
		t.Run(pwd, func(t *testing.T) {
			cfg := baseConfig()
			cfg.Pwd = pwd
			dsn, err := buildDSN(cfg)
			if err != nil {
				t.Fatalf("buildDSN: %v", err)
			}
			parsed, err := gdbmysql.ParseDSN(dsn)
			if err != nil {
				t.Fatalf("ParseDSN(%q): %v", dsn, err)
			}
			if parsed.Passwd != pwd {
				t.Fatalf("password round-tripped as %q, want %q (dsn=%q)", parsed.Passwd, pwd, dsn)
			}
		})
	}
}

func TestBuildDSNColumnsNameToLowerCanBeDisabled(t *testing.T) {
	cfg := baseConfig()
	cfg.Extra[ExtraColumnsNameToLower] = "false"

	dsn, err := buildDSN(cfg)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	parsed, err := gdbmysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	if parsed.ColumnsNameToLower {
		t.Fatal("ColumnsNameToLower = true, want false")
	}
}

func TestBuildDSNRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*dao.Config)
		want  string
	}{
		{
			name:  "empty address",
			apply: func(c *dao.Config) { c.Addr = "" },
			want:  "address is empty",
		},
		{
			name:  "unknown mode",
			apply: func(c *dao.Config) { c.Extra[ExtraMode] = "quorum" },
			want:  "unsupported mode",
		},
		{
			name: "random spelled as a loadbalancemode",
			apply: func(c *dao.Config) {
				c.Addr = "10.0.0.1:3306,10.0.0.2:3306"
				c.Extra[ExtraMode] = "loadbalancemode=random"
			},
			want: "unsupported mode",
		},
		{
			name: "single mode with several addresses",
			apply: func(c *dao.Config) {
				c.Addr = "10.0.0.1:3306,10.0.0.2:3306"
				c.Extra[ExtraMode] = ModeSingle
			},
			want: "accepts one address",
		},
		{
			name: "master_cn without port",
			apply: func(c *dao.Config) {
				c.Addr = "goldendb-cn-1"
				c.Extra[ExtraMode] = ModeMasterCN
			},
			want: "explicit port",
		},
		{
			name: "proxy group member without port",
			apply: func(c *dao.Config) {
				c.Addr = "10.0.0.1:3306"
				c.Extra[ExtraMode] = ModeMasterCN
				c.Extra[ExtraProxyGroups] = "10.0.1.1"
			},
			want: "must be host:port",
		},
		{
			name: "proxy groups in single mode",
			apply: func(c *dao.Config) {
				c.Extra[ExtraMode] = ModeSingle
				c.Extra[ExtraProxyGroups] = "10.0.1.1:3306"
			},
			want: "requires a multi-CN mode",
		},
		{
			name:  "non-numeric blacklist",
			apply: func(c *dao.Config) { c.Extra[ExtraBlackListMS] = "soon" },
			want:  "invalid blacklist_ms",
		},
		{
			name:  "negative min connection proxys",
			apply: func(c *dao.Config) { c.Extra[ExtraMinConnectionProxys] = "-2" },
			want:  "invalid min_connection_proxys",
		},
		{
			name:  "invalid columnsNameToLower",
			apply: func(c *dao.Config) { c.Extra[ExtraColumnsNameToLower] = "maybe" },
			want:  "invalid columns_name_to_lower",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.apply(&cfg)
			if _, err := buildDSN(cfg); err == nil {
				t.Fatal("buildDSN succeeded, want an error")
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestSessionLockerSharesTheMySQLLockName(t *testing.T) {
	d := &driver{}
	locker, ok := d.SessionLocker(dao.Config{MigrationLockTimeoutSeconds: 7}).(*sessionLocker)
	if !ok {
		t.Fatalf("SessionLocker type = %T", d.SessionLocker(dao.Config{}))
	}
	if locker.name != globalLockName {
		t.Fatalf("lock name = %q, want %q", locker.name, globalLockName)
	}
	if locker.name != "cubemaster_schema_migration_global" {
		t.Fatalf("lock name drifted from the MySQL driver: %q", locker.name)
	}
	if locker.timeout != 7 {
		t.Fatalf("timeout = %d, want 7", locker.timeout)
	}
}

func TestDriverName(t *testing.T) {
	if got := (&driver{}).Name(); got != "goldendb" {
		t.Fatalf("Name() = %q", got)
	}
	if gdbmysql.DriverName != DriverName {
		t.Fatalf("vendored driver registers %q, wrapper expects %q", gdbmysql.DriverName, DriverName)
	}
}
