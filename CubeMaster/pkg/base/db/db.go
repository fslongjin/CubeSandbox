// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

// Package db provides database access.
package db

import (
	"errors"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dao"
	"github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dao/driver/goldendb"
	"gorm.io/gorm"
)

// Init returns the global dao handle. Retained for backwards compatibility
// with v0.2.2-era callers (nodemeta / localcache / instancecache /
// templatecenter) that still pass a DBConfig. cfg is accepted but ignored —
// the connection is already established by dao.Open before any business
// package Init runs.
func Init(cfg *config.DBConfig) *gorm.DB {
	_ = cfg
	return dao.Default()
}

// ConfigFromDBConfig maps a config.DBConfig to the dao.Config used to open
// the shared database handle. dao.Open keys its idempotence on this config
// identity, so every call site that opens the handle before app startup
// (schema init, integration/mock-debug bootstrap) must build the dao config
// through this helper — keeping the two mappings from drifting.
func ConfigFromDBConfig(src *config.DBConfig) (dao.Config, error) {
	if src == nil {
		return dao.Config{}, errors.New("db config is nil")
	}
	return dao.Config{
		Driver:                      src.Driver,
		Addr:                        src.Addr,
		User:                        src.User,
		Pwd:                         src.Pwd,
		DBName:                      src.DBName,
		ConnTimeoutSeconds:          src.ConnTimeout,
		ReadTimeoutSeconds:          src.ReadTimeout,
		WriteTimeoutSeconds:         src.WriteTimeout,
		MaxIdleConns:                src.MaxIdleConns,
		MaxOpenConns:                src.MaxOpenConns,
		MaxConnLifeTimeSeconds:      src.MaxConnLifeTimeSeconds,
		MigrationLockTimeoutSeconds: src.MigrationLockTimeoutSeconds,
		Extra:                       EngineExtra(src),
	}, nil
}

// EngineExtra translates the engine-specific yaml block into the Extra map
// the dao driver reads. Today only GoldenDB needs one; other engines get nil
// so their config identity stays exactly as before.
func EngineExtra(src *config.DBConfig) map[string]string {
	if src == nil || src.GoldenDB == nil {
		return nil
	}
	g := src.GoldenDB
	return goldendb.Settings{
		Mode:                g.Mode,
		BlackListMS:         g.BlackListMS,
		MasterBlackListMS:   g.MasterBlackListMS,
		ProxyGroups:         g.ProxyGroups,
		MinConnectionProxys: g.MinConnectionProxys,
		ColumnsNameToLower:  g.ColumnsNameToLower,
	}.Extra()
}
