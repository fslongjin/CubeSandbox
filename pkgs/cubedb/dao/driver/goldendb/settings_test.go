// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package goldendb

import (
	"reflect"
	"testing"
)

func TestSettingsExtraRendersEveryKey(t *testing.T) {
	blacklist := int64(500)
	masterBlacklist := int64(-1)
	lower := false
	got := Settings{
		Mode:                ModeMasterCN,
		BlackListMS:         &blacklist,
		MasterBlackListMS:   &masterBlacklist,
		ProxyGroups:         []string{"10.0.1.1:3306,10.0.1.2:3306", "10.1.0.1:3306"},
		MinConnectionProxys: 1,
		ColumnsNameToLower:  &lower,
	}.Extra()

	want := map[string]string{
		ExtraMode:                ModeMasterCN,
		ExtraBlackListMS:         "500",
		ExtraMasterBlackListMS:   "-1",
		ExtraProxyGroups:         "10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306",
		ExtraMinConnectionProxys: "1",
		ExtraColumnsNameToLower:  "false",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Extra mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

// TestSettingsExtraIsNilWhenEmpty keeps plain MySQL and PostgreSQL config
// identities unchanged: an empty GoldenDB block must not add an Extra entry
// that would make dao.Open treat the config as different.
func TestSettingsExtraIsNilWhenEmpty(t *testing.T) {
	if got := (Settings{}).Extra(); got != nil {
		t.Fatalf("Extra() = %+v, want nil for empty settings", got)
	}
	if got := (Settings{Mode: "  "}).Extra(); got != nil {
		t.Fatalf("Extra() = %+v, want nil for whitespace-only mode", got)
	}
}

func TestJoinProxyGroupsDropsEmptyGroups(t *testing.T) {
	got := JoinProxyGroups([]string{"10.0.1.1:3306", "", " ", "10.1.0.1:3306"})
	want := "10.0.1.1:3306;10.1.0.1:3306"
	if got != want {
		t.Fatalf("JoinProxyGroups = %q, want %q", got, want)
	}
	if got := JoinProxyGroups(nil); got != "" {
		t.Fatalf("JoinProxyGroups(nil) = %q, want empty", got)
	}
}
