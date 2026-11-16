# Vendored GoldenDB Go SQL driver

This directory is a vendored copy of the GoldenDB Go SQL driver shipped by
ZTE as `go_sql_driver_V1.0.07.zip` (`mysql-master/`). It is a fork of
[go-sql-driver/mysql](https://github.com/go-sql-driver/mysql) at roughly
v1.7.1 with GoldenDB-specific additions:

- comma-separated CN address lists in the DSN: `tcp(cn1:3306,cn2:3306)`
- `loadbalance=true` random load balancing
- `loadbalancemode=masterslave` master/slave detection via the handshake
  `master_flag` and `select @@global.cluster_master_host`
- `masterConnection=true` master CN mode
- `blacklist` / `masterblacklist` node blacklisting (milliseconds)
- `proxygroups`, `minconnectionproxys`, `proxygroupN` priority groups
- `columnsNameToLower` result-set column lowercasing

## Why it is vendored instead of `replace`d

The vendor instructions ask for
`replace github.com/go-sql-driver/mysql => ./mysql-master`. Cube cannot do
that: `CubeMaster` imports the upstream driver directly for
`*mysql.MySQLError` classification, `gorm.io/driver/mysql` pins the upstream
module, and Go cannot link two versions of one module path. Replacing the
module would downgrade every plain MySQL deployment (built-in MySQL 8, Helm,
one-click) onto this older fork.

Two changes were made to the vendored sources:

1. `go.mod` module path is
   `github.com/tencentcloud/CubeSandbox/pkgs/third_party/goldendb-go-sql-driver`.
2. The driver registers as `goldendb` (`DriverName`) instead of `mysql`, so
   both drivers coexist in one process. `master_connection.go` uses that
   constant for its internal master discovery connections.

Everything else, including the GoldenDB DSN parsing, is unchanged.

## Using it

Import the driver for its side effect and open with the `goldendb` name. The
`pkgs/cubedb/dao/driver/goldendb` package wraps it for Cube:

```go
import (
	"database/sql"

	_ "github.com/tencentcloud/CubeSandbox/pkgs/third_party/goldendb-go-sql-driver"
)

db, err := sql.Open("goldendb", "user:pass@tcp(cn1:3306,cn2:3306)/dbname?loadbalance=true")
```

GoldenDB parameters must reach the DSN as plain query text. Do not build them
through `Config.FormatDSN`: it drops `masterConnection` and the proxy group
settings, and it percent-escapes the commas in `proxygroupN` address lists,
which the parser does not unescape.

## Licence

MPL-2.0, see [LICENSE](LICENSE). The MPL copyleft covers modified files, which
are published here as source. `README.md` and `CHANGELOG.md` are the vendor's
originals; `GDB_update.md` is the vendor's change log.
