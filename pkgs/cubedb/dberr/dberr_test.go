// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package dberr_test

import (
	"errors"
	"fmt"
	"testing"

	upstreammysql "github.com/go-sql-driver/mysql"
	"github.com/tencentcloud/CubeSandbox/pkgs/cubedb/dberr"
	goldendbmysql "github.com/tencentcloud/CubeSandbox/pkgs/third_party/goldendb-go-sql-driver"
)

// TestIsDuplicateEntryMatchesBothDrivers is the regression this package exists
// for: the upstream and vendored GoldenDB drivers each declare their own
// MySQLError type, so a single-driver errors.As assertion misses one of them.
func TestIsDuplicateEntryMatchesBothDrivers(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "upstream 1062",
			err:  &upstreammysql.MySQLError{Number: dberr.NumberDuplicateEntry, Message: "Duplicate entry"},
			want: true,
		},
		{
			name: "goldendb 1062",
			err:  &goldendbmysql.MySQLError{Number: dberr.NumberDuplicateEntry, Message: "Duplicate entry"},
			want: true,
		},
		{
			name: "wrapped upstream 1062",
			err:  fmt.Errorf("claim alias: %w", &upstreammysql.MySQLError{Number: dberr.NumberDuplicateEntry}),
			want: true,
		},
		{
			name: "wrapped goldendb 1062",
			err:  fmt.Errorf("claim alias: %w", &goldendbmysql.MySQLError{Number: dberr.NumberDuplicateEntry}),
			want: true,
		},
		{
			name: "upstream 1406 is not a duplicate",
			err:  &upstreammysql.MySQLError{Number: 1406, Message: "Data too long"},
			want: false,
		},
		{
			name: "plain error",
			err:  errors.New("connection refused"),
			want: false,
		},
		{name: "nil", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dberr.IsDuplicateEntry(tt.err); got != tt.want {
				t.Fatalf("IsDuplicateEntry(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsLockConflictMatchesBothDrivers(t *testing.T) {
	for _, number := range []uint16{dberr.NumberDeadlock, dberr.NumberLockWaitTimeout} {
		upstream := &upstreammysql.MySQLError{Number: number}
		goldendb := &goldendbmysql.MySQLError{Number: number}
		if !dberr.IsLockConflict(upstream) {
			t.Fatalf("upstream %d not classified as a lock conflict", number)
		}
		if !dberr.IsLockConflict(goldendb) {
			t.Fatalf("goldendb %d not classified as a lock conflict", number)
		}
	}
	if dberr.IsLockConflict(&goldendbmysql.MySQLError{Number: dberr.NumberDuplicateEntry}) {
		t.Fatal("duplicate entry must not be classified as a lock conflict")
	}
	if dberr.IsLockConflict(errors.New("Error 1213: Deadlock found")) {
		t.Fatal("a plain error must not be classified as a lock conflict")
	}
}

func TestMySQLNumberReportsTheNumber(t *testing.T) {
	got, ok := dberr.MySQLNumber(&goldendbmysql.MySQLError{Number: 1234})
	if !ok || got != 1234 {
		t.Fatalf("MySQLNumber = %d, %v", got, ok)
	}
	if _, ok := dberr.MySQLNumber(errors.New("x")); ok {
		t.Fatal("MySQLNumber reported a number for a non-MySQL error")
	}
}
