// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

package dao

import "testing"

// TestNormalizeAddrKeepsCNList guards the goldendb path: dao.Config.Addr holds a
// comma-separated CN list, and normalizeAddr must pass it through untouched.
// SplitHostPort splits on the last colon, so it succeeds with a non-empty host
// for such a list; wrapping it with JoinHostPort("localhost", ...) would turn
// the CN list into a single bogus endpoint.
func TestNormalizeAddrKeepsCNList(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{
			name: "single host port is unchanged",
			addr: "10.0.0.11:3306",
			want: "10.0.0.11:3306",
		},
		{
			name: "goldendb CN list is unchanged",
			addr: "10.0.0.11:3306,10.0.0.12:3306",
			want: "10.0.0.11:3306,10.0.0.12:3306",
		},
		{
			name: "port-only is completed with localhost",
			addr: ":3306",
			want: "localhost:3306",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAddr(tt.addr); got != tt.want {
				t.Fatalf("normalizeAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
