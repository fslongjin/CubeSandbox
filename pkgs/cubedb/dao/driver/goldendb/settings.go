// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package goldendb

import (
	"strconv"
	"strings"
)

// Settings is the engine-specific configuration every front end (CubeMaster's
// yaml block, CubeOps' yaml/env block) collects. It mirrors dao.Config.Extra,
// so callers describe GoldenDB once and Settings.Extra renders it with the key
// names this driver reads.
//
// Pointer and zero-value semantics match the yaml blocks: nil means "leave the
// driver default", and an explicit false/0 is distinguishable from unset where
// that matters.
type Settings struct {
	// Mode is single, random, masterslave or master_cn; empty means
	// masterslave.
	Mode string
	// BlackListMS blacklists a failing CN for this many milliseconds.
	BlackListMS *int64
	// MasterBlackListMS is the master CN variant of BlackListMS.
	MasterBlackListMS *int64
	// ProxyGroups lists priority groups, highest priority first. Each entry is
	// a comma-separated list of host:port members.
	ProxyGroups []string
	// MinConnectionProxys is the healthy-CN count a group needs before the next
	// group participates. Zero means the driver default.
	MinConnectionProxys int
	// ColumnsNameToLower lowercases result-set column names. nil means the
	// driver default (true).
	ColumnsNameToLower *bool
}

// Extra renders the settings as dao.Config.Extra. It returns nil when nothing
// was set, so a plain-MySQL config identity stays byte-identical to the value
// it produced before the GoldenDB block existed.
func (s Settings) Extra() map[string]string {
	extra := map[string]string{}
	if v := strings.TrimSpace(s.Mode); v != "" {
		extra[ExtraMode] = v
	}
	if s.BlackListMS != nil {
		extra[ExtraBlackListMS] = strconv.FormatInt(*s.BlackListMS, 10)
	}
	if s.MasterBlackListMS != nil {
		extra[ExtraMasterBlackListMS] = strconv.FormatInt(*s.MasterBlackListMS, 10)
	}
	if len(s.ProxyGroups) > 0 {
		extra[ExtraProxyGroups] = JoinProxyGroups(s.ProxyGroups)
	}
	if s.MinConnectionProxys > 0 {
		extra[ExtraMinConnectionProxys] = strconv.Itoa(s.MinConnectionProxys)
	}
	if s.ColumnsNameToLower != nil {
		extra[ExtraColumnsNameToLower] = strconv.FormatBool(*s.ColumnsNameToLower)
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

// JoinProxyGroups renders priority groups as the single string ExtraProxyGroups
// carries: groups separated by ';', members by ','. Empty groups are dropped.
func JoinProxyGroups(groups []string) string {
	cleaned := make([]string, 0, len(groups))
	for _, group := range groups {
		if g := strings.TrimSpace(group); g != "" {
			cleaned = append(cleaned, g)
		}
	}
	return strings.Join(cleaned, ";")
}
