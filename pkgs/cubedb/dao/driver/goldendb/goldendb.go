// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

// Package goldendb plugs GoldenDB into CubeDB/dao. GoldenDB speaks the MySQL
// wire protocol, so GORM and goose keep using the MySQL dialect and the
// MySQL migration set; only connection establishment and DSN composition
// differ.
//
// The engine itself is provided by the vendored driver in
// pkgs/third_party/goldendb-go-sql-driver, which registers the "goldendb"
// database/sql driver name. That fork is based on go-sql-driver/mysql v1.7.x,
// while plain MySQL deployments keep using the upstream module, so both can
// be linked into one binary.
package goldendb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3/lock"
	"github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dao"
	gdbmysql "github.com/tencentcloud/CubeSandbox/pkgs/third_party/goldendb-go-sql-driver"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const (
	// DriverName is the canonical short name. It doubles as the migrations
	// sub-directory name passed to the migrator, which aliases it onto the
	// MySQL migration set.
	DriverName = "goldendb"

	// globalLockName must match the plain MySQL driver. CubeMaster,
	// CubeTemplateCenter and CubeOps share one goose history against the same
	// schema, so they must all queue on the same cluster-side lock even when
	// one of them is configured for GoldenDB.
	globalLockName = "cubemaster_schema_migration_global"

	defaultLockTimeoutSeconds = 60
)

// Connection modes. The zero value resolves to ModeMasterSlave.
const (
	// ModeSingle dials the single address in the DSN and skips every
	// GoldenDB extension. Use it only for a plain single-node endpoint.
	ModeSingle = "single"
	// ModeRandom spreads connections across the configured CNs
	// (loadbalance=true). A migration lock acquired on a random CN is not
	// cluster-wide, so prefer another mode for the control plane.
	ModeRandom = "random"
	// ModeMasterSlave detects the master CN from the handshake master flag
	// and falls back to select @@global.cluster_master_host.
	ModeMasterSlave = "masterslave"
	// ModeMasterCN always dials the master CN (masterConnection=true).
	ModeMasterCN = "master_cn"
)

// Extra keys read from dao.Config.Extra. They carry the settings that have no
// home in dao.Config's stable shape.
const (
	// ExtraMode is one of ModeSingle/ModeRandom/ModeMasterSlave/ModeMasterCN.
	ExtraMode = "mode"
	// ExtraBlackListMS blacklists a failing CN for this many milliseconds.
	// GoldenDB's own default is 200; -1 disables the blacklist.
	ExtraBlackListMS = "blacklist_ms"
	// ExtraMasterBlackListMS is the master-CN variant of ExtraBlackListMS.
	ExtraMasterBlackListMS = "master_blacklist_ms"
	// ExtraProxyGroups lists priority groups as semicolon-separated
	// comma-separated address lists, highest priority first:
	// "10.0.0.1:3306,10.0.0.2:3306;10.1.0.1:3306". Comma separates members
	// because the DSN uses ';' between groups; a group's members must be
	// explicit host:port.
	ExtraProxyGroups = "proxy_groups"
	// ExtraMinConnectionProxys is the minimum number of healthy CNs a group
	// must have before the next group participates.
	ExtraMinConnectionProxys = "min_connection_proxys"
	// ExtraColumnsNameToLower lowercases result-set column names. Defaults to
	// true; goose and the fingerprint layer scan by position so they are
	// unaffected, but named GORM scans are.
	ExtraColumnsNameToLower = "columns_name_to_lower"
)

func init() {
	dao.Register(&driver{})
}

type driver struct{}

func (d *driver) Name() string { return DriverName }

func (d *driver) Open(ctx context.Context, cfg dao.Config) (*sql.DB, *gorm.DB, error) {
	_ = ctx
	dsn, err := buildDSN(cfg)
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := sql.Open(gdbmysql.DriverName, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("sql.Open: %w", err)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxConnLifeTimeSeconds > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxConnLifeTimeSeconds) * time.Second)
	}
	// Pass the existing pool instead of a DSN: gorm's mysql dialector only
	// opens its own *sql.DB when DSN/DSNConfig is set, so the GoldenDB driver
	// stays in charge of every connection. Dialector.Name() stays "mysql",
	// which is what the dialect-specific SQL paths expect.
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("gorm.Open: %w", err)
	}
	return sqlDB, gormDB, nil
}

func (d *driver) SessionLocker(cfg dao.Config) lock.SessionLocker {
	timeout := cfg.MigrationLockTimeoutSeconds
	if timeout <= 0 {
		timeout = defaultLockTimeoutSeconds
	}
	return &sessionLocker{name: globalLockName, timeout: timeout}
}

type options struct {
	mode                string
	addrs               []string
	blackListMS         int64
	blackListSet        bool
	masterBlackListMS   int64
	masterBlackListSet  bool
	proxyGroups         [][]string
	minConnectionProxys int
	columnsNameToLower  bool
}

// buildDSN composes the DSN from dao.Config plus the GoldenDB options in
// Extra. The GoldenDB parameters are appended as raw query text rather than
// round-tripped through the fork's Config.FormatDSN, because FormatDSN drops
// masterConnection and the proxy group settings, and it percent-escapes the
// commas inside proxygroupN addresses that parseDSNParams reads back without
// unescaping.
func buildDSN(cfg dao.Config) (string, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return "", err
	}

	mc := gdbmysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Pwd
	mc.Net = "tcp"
	mc.Addr = strings.Join(opts.addrs, ",")
	mc.DBName = cfg.DBName
	mc.Timeout = durationOrDefault(cfg.ConnTimeoutSeconds)
	mc.ReadTimeout = durationOrDefault(cfg.ReadTimeoutSeconds)
	mc.WriteTimeout = durationOrDefault(cfg.WriteTimeoutSeconds)
	mc.ParseTime = true
	mc.Loc = time.Local
	mc.ColumnsNameToLower = opts.columnsNameToLower
	mc.Params = map[string]string{"charset": "utf8mb4"}

	dsn := mc.FormatDSN()
	extra := opts.dsnParams()
	if len(extra) > 0 {
		dsn += "&" + strings.Join(extra, "&")
	}
	return dsn, nil
}

// dsnParams renders the GoldenDB-specific query parameters. They are written
// verbatim: the fork parses proxy group addresses without unescaping, and the
// addresses contain commas.
func (o options) dsnParams() []string {
	var params []string
	switch o.mode {
	case ModeRandom:
		// Random balancing is loadbalance alone. loadbalancemode only accepts
		// "masterslave" and would reject "random".
		params = append(params, "loadbalance=true")
	case ModeMasterSlave:
		params = append(params, "loadbalancemode=masterslave")
	case ModeMasterCN:
		// Mutually exclusive with loadbalance; the fork rejects both at once.
		params = append(params, "masterConnection=true")
	}

	if o.blackListSet {
		params = append(params, "blacklist="+strconv.FormatInt(o.blackListMS, 10))
	}
	if o.masterBlackListSet {
		params = append(params, "masterblacklist="+strconv.FormatInt(o.masterBlackListMS, 10))
	}
	if len(o.proxyGroups) > 0 {
		params = append(params, "proxygroups="+strconv.Itoa(len(o.proxyGroups)))
		if o.minConnectionProxys > 0 {
			params = append(params, "minconnectionproxys="+strconv.Itoa(o.minConnectionProxys))
		}
		for i, group := range o.proxyGroups {
			params = append(params, "proxygroup"+strconv.Itoa(i+1)+"="+strings.Join(group, ","))
		}
	}
	return params
}

func parseOptions(cfg dao.Config) (options, error) {
	opts := options{columnsNameToLower: true}

	addrs := splitAddrs(cfg.Addr)
	if len(addrs) == 0 {
		return opts, errors.New("goldendb: address is empty")
	}
	opts.addrs = addrs

	mode := strings.ToLower(strings.TrimSpace(cfg.Extra[ExtraMode]))
	if mode == "" {
		// The control plane creates and migrates schema, so it must land on
		// the master CN by default. A single address still goes through the
		// master-flag check, which is what a GoldenDB deployment expects.
		mode = ModeMasterSlave
	}
	switch mode {
	case ModeSingle, ModeRandom, ModeMasterSlave, ModeMasterCN:
	default:
		return opts, fmt.Errorf("goldendb: unsupported mode %q (want single, random, masterslave or master_cn)", mode)
	}
	opts.mode = mode

	if mode == ModeSingle && len(addrs) > 1 {
		return opts, fmt.Errorf("goldendb: mode=single accepts one address, got %d", len(addrs))
	}

	if mode == ModeMasterCN {
		for _, addr := range addrs {
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return opts, fmt.Errorf("goldendb: master_cn requires an explicit port on every address, got %q", addr)
			}
		}
	}

	if v, ok := cfg.Extra[ExtraBlackListMS]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return opts, fmt.Errorf("goldendb: invalid %s %q: %w", ExtraBlackListMS, v, err)
		}
		opts.blackListMS, opts.blackListSet = n, true
	}
	if v, ok := cfg.Extra[ExtraMasterBlackListMS]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return opts, fmt.Errorf("goldendb: invalid %s %q: %w", ExtraMasterBlackListMS, v, err)
		}
		opts.masterBlackListMS, opts.masterBlackListSet = n, true
	}
	if v, ok := cfg.Extra[ExtraMinConnectionProxys]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return opts, fmt.Errorf("goldendb: invalid %s %q: want a non-negative integer", ExtraMinConnectionProxys, v)
		}
		opts.minConnectionProxys = n
	}
	if v, ok := cfg.Extra[ExtraProxyGroups]; ok && strings.TrimSpace(v) != "" {
		groups, err := parseProxyGroups(v)
		if err != nil {
			return opts, err
		}
		opts.proxyGroups = groups
	}
	if len(opts.proxyGroups) > 0 && mode == ModeSingle {
		return opts, fmt.Errorf("goldendb: %s requires a multi-CN mode, not single", ExtraProxyGroups)
	}
	if v, ok := cfg.Extra[ExtraColumnsNameToLower]; ok && strings.TrimSpace(v) != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return opts, fmt.Errorf("goldendb: invalid %s %q: %w", ExtraColumnsNameToLower, v, err)
		}
		opts.columnsNameToLower = b
	}
	return opts, nil
}

// parseProxyGroups accepts "g1a,g1b;g2a,g2b" and validates that every member
// names an explicit port. The fork requires ports for master CN groups and
// silently mismatches DNS names that resolve to more than one IP.
func parseProxyGroups(raw string) ([][]string, error) {
	var groups [][]string
	for _, chunk := range strings.Split(raw, ";") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		members := splitAddrs(chunk)
		if len(members) == 0 {
			continue
		}
		for _, member := range members {
			if _, _, err := net.SplitHostPort(member); err != nil {
				return nil, fmt.Errorf("goldendb: proxy group member %q must be host:port", member)
			}
		}
		groups = append(groups, members)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("goldendb: %s is set but contains no addresses", ExtraProxyGroups)
	}
	return groups, nil
}

func splitAddrs(raw string) []string {
	var addrs []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			addrs = append(addrs, part)
		}
	}
	return addrs
}

func durationOrDefault(seconds int) time.Duration {
	if seconds <= 0 {
		return 5 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// sessionLocker implements goose.SessionLocker with MySQL's GET_LOCK. GoldenDB
// exposes the same builtin. The lock is session-scoped, so a crashed process
// releases it automatically; there is no janitor to run.
type sessionLocker struct {
	name    string
	timeout int
}

// SessionLock blocks for up to s.timeout seconds. GET_LOCK returning 0 means
// the wait expired, which is a hard error: another instance is still
// migrating, and failing fast surfaces the queue instead of skipping work.
func (s *sessionLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx,
		"SELECT GET_LOCK(?, ?)", s.name, s.timeout).Scan(&got); err != nil {
		return fmt.Errorf("acquire migration lock %q: %w", s.name, err)
	}
	if !got.Valid {
		return fmt.Errorf("acquire migration lock %q: returned NULL (connection killed?)", s.name)
	}
	if got.Int64 != 1 {
		return fmt.Errorf("acquire migration lock %q: timeout after %ds", s.name, s.timeout)
	}
	return nil
}

// SessionUnlock is best-effort: a dead connection already dropped the lock,
// and reporting that would mask the real failure that preceded it.
func (s *sessionLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "DO RELEASE_LOCK(?)", s.name)
	return err
}
