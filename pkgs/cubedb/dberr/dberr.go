// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

// Package dberr classifies SQL driver errors without committing callers to one
// MySQL driver implementation.
//
// Cube links two independent MySQL wire-protocol drivers:
//
//   - github.com/go-sql-driver/mysql (upstream), used for plain MySQL, owned by
//     the "mysql" dao driver;
//   - the vendored GoldenDB fork, owned by the "goldendb" dao driver.
//
// Each declares its own *mysql.MySQLError type, so an errors.As assertion
// written against one of them silently fails for connections opened by the
// other. That matters where the classification decides behaviour: a missed
// duplicate-key turns a retryable race into a 500, and a missed deadlock skips
// the retry. This package asserts both.
package dberr

import (
	"errors"

	upstreammysql "github.com/go-sql-driver/mysql"
	goldendbmysql "github.com/tencentcloud/CubeSandbox/pkgs/third_party/goldendb-go-sql-driver"
)

// Common MySQL server error numbers used by the callers.
const (
	// NumberLockWaitTimeout is ER_LOCK_WAIT_TIMEOUT: the statement gave up
	// waiting for a row lock.
	NumberLockWaitTimeout uint16 = 1205
	// NumberDeadlock is ER_LOCK_DEADLOCK: InnoDB rolled the transaction back.
	NumberDeadlock uint16 = 1213
	// NumberDuplicateEntry is ER_DUP_ENTRY: a unique index rejected the row.
	NumberDuplicateEntry uint16 = 1062
)

// MySQLNumber returns the MySQL server error number carried by err, matching
// both the upstream driver and the vendored GoldenDB fork. It unwraps the error
// chain, so a wrapped MySQLError is still recognised.
func MySQLNumber(err error) (uint16, bool) {
	if err == nil {
		return 0, false
	}
	var upstreamErr *upstreammysql.MySQLError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.Number, true
	}
	var goldenErr *goldendbmysql.MySQLError
	if errors.As(err, &goldenErr) {
		return goldenErr.Number, true
	}
	return 0, false
}

// IsMySQLNumber reports whether err is a MySQL server error with the given
// number, from either driver.
func IsMySQLNumber(err error, numbers ...uint16) bool {
	n, ok := MySQLNumber(err)
	if !ok {
		return false
	}
	for _, want := range numbers {
		if n == want {
			return true
		}
	}
	return false
}

// IsDuplicateEntry reports a unique-constraint violation (ER_DUP_ENTRY).
func IsDuplicateEntry(err error) bool {
	return IsMySQLNumber(err, NumberDuplicateEntry)
}

// IsLockConflict reports a transient lock failure worth one retry:
// ER_LOCK_WAIT_TIMEOUT or ER_LOCK_DEADLOCK.
func IsLockConflict(err error) bool {
	return IsMySQLNumber(err, NumberLockWaitTimeout, NumberDeadlock)
}
