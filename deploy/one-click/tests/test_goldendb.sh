#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Tencent. All rights reserved.
#
# Guard tests for the one-click GoldenDB path: the CN list must survive into
# instance_db_config.addr (never collapsed to a single host:port), the
# goldendb: sub-block must be written and later scrubbed on an engine switch,
# and validation must reject inconsistent CUBE_EXTERNAL_GOLDENDB_* input.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ONE_CLICK_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

# shellcheck source=../lib/common.sh
source "${ONE_CLICK_DIR}/lib/common.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# Run a helper in a clean subshell: no leftover CUBE_EXTERNAL_* / driver vars
# from the caller may flip the engine branch. Leading VAR=value arguments are
# exported; the first non-assignment and everything after it is the command.
run_clean() {
  (
    unset CUBE_DATABASE_DRIVER \
      CUBE_EXTERNAL_MYSQL_HOST CUBE_EXTERNAL_MYSQL_PORT CUBE_EXTERNAL_MYSQL_USER \
      CUBE_EXTERNAL_MYSQL_PASSWORD CUBE_EXTERNAL_MYSQL_DB \
      CUBE_EXTERNAL_POSTGRES_HOST CUBE_EXTERNAL_POSTGRES_PORT CUBE_EXTERNAL_POSTGRES_USER \
      CUBE_EXTERNAL_POSTGRES_PASSWORD CUBE_EXTERNAL_POSTGRES_DB \
      CUBE_EXTERNAL_GOLDENDB_ADDRS CUBE_EXTERNAL_GOLDENDB_USER \
      CUBE_EXTERNAL_GOLDENDB_PASSWORD CUBE_EXTERNAL_GOLDENDB_DB \
      CUBE_EXTERNAL_GOLDENDB_MODE CUBE_EXTERNAL_GOLDENDB_PROXY_GROUPS \
      CUBE_EXTERNAL_GOLDENDB_MIN_CONNECTION_PROXYS
    # shellcheck disable=SC1091
    source "${ONE_CLICK_DIR}/lib/common.sh"

    local env_args=()
    local args=("$@")
    while [[ "${#args[@]}" -gt 0 && "${args[0]}" == *=* ]]; do
      env_args+=("${args[0]}")
      args=("${args[@]:1}")
    done
    if [[ "${#env_args[@]}" -gt 0 ]]; then
      export "${env_args[@]}"
    fi
    "${args[@]}"
  )
}

write_shipped_conf() {
  local name="$1"
  cat > "${TMP_DIR}/${name}" <<'EOF'
instance_db_config:
  driver: "mysql"
  addr: "127.0.0.1:3306"
  user: "cube"
  pwd: "cube_pass"
  db_name: "cube_mvp"
  conn_timeout: 5
  max_open_conns: 20
redis:
  nodes: "127.0.0.1:6379"
EOF
  printf '%s' "${TMP_DIR}/${name}"
}

test_goldendb_patch_keeps_cn_list() {
  local cfg
  cfg="$(write_shipped_conf goldendb.yaml)"
  run_clean \
    CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306,10.0.0.12:3306' \
    CUBE_EXTERNAL_GOLDENDB_USER=gdbuser \
    CUBE_EXTERNAL_GOLDENDB_PASSWORD='gdb/pass|x' \
    CUBE_EXTERNAL_GOLDENDB_DB=cube_ext \
    CUBE_EXTERNAL_GOLDENDB_MODE=master_cn \
    CUBE_EXTERNAL_GOLDENDB_PROXY_GROUPS='10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306' \
    CUBE_EXTERNAL_GOLDENDB_MIN_CONNECTION_PROXYS=1 \
    patch_conf_external_instance_db "${cfg}"

  grep -Fq 'driver: "goldendb"' "${cfg}" || fail "driver not patched to goldendb"
  grep -Fq 'addr: "10.0.0.11:3306,10.0.0.12:3306"' "${cfg}" \
    || fail "CN list must stay comma separated in instance_db_config.addr"
  grep -Fq 'user: "gdbuser"' "${cfg}" || fail "user not patched"
  grep -Fq 'pwd: "gdb/pass|x"' "${cfg}" || fail "pwd not written intact"
  grep -Fq 'db_name: "cube_ext"' "${cfg}" || fail "db_name not patched"
  # The sub-block lives under instance_db_config (two-space indent).
  grep -Fq '  goldendb:' "${cfg}" || fail "goldendb sub-block missing"
  grep -Fq '    mode: "master_cn"' "${cfg}" || fail "mode missing from goldendb sub-block"
  grep -Fq '    - "10.0.0.11:3306,10.0.0.12:3306"' "${cfg}" \
    && fail "the CN list must not be duplicated into proxy_groups"
  grep -Fq '    - "10.0.1.1:3306,10.0.1.2:3306"' "${cfg}" || fail "proxy group 1 missing"
  grep -Fq '    - "10.1.0.1:3306"' "${cfg}" || fail "proxy group 2 missing"
  grep -Fq '    min_connection_proxys: 1' "${cfg}" || fail "min_connection_proxys missing"
  # Nothing outside instance_db_config may be touched.
  grep -Fq 'redis:' "${cfg}" || fail "redis block must survive"
  grep -Fq '  conn_timeout: 5' "${cfg}" || fail "conn_timeout must survive"
}

# A switch from GoldenDB back to MySQL must delete the whole sub-block, not
# leave an orphaned mode/proxy setting behind for the next engine.
test_goldendb_block_removed_on_mysql_switch() {
  local cfg
  cfg="$(write_shipped_conf switch.yaml)"
  run_clean \
    CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306' \
    CUBE_EXTERNAL_GOLDENDB_MODE=masterslave \
    patch_conf_external_instance_db "${cfg}"
  grep -Fq '  goldendb:' "${cfg}" || fail "setup: goldendb sub-block not written"

  run_clean \
    CUBE_EXTERNAL_MYSQL_HOST=mysql.example.internal \
    CUBE_EXTERNAL_MYSQL_USER=extuser \
    CUBE_EXTERNAL_MYSQL_DB=cube_ext \
    patch_conf_external_instance_db "${cfg}"

  grep -Fq 'driver: "mysql"' "${cfg}" || fail "driver not switched back to mysql"
  grep -Fq 'addr: "mysql.example.internal:3306"' "${cfg}" || fail "mysql addr not patched"
  if grep -Fq 'goldendb:' "${cfg}"; then
    fail "goldendb sub-block must be removed when switching to MySQL"
  fi
  grep -Fq '  conn_timeout: 5' "${cfg}" || fail "conn_timeout must survive the removal"
  grep -Fq 'redis:' "${cfg}" || fail "redis block must survive the removal"
}

# Repeated GoldenDB patches must not stack sub-blocks.
test_goldendb_patch_is_idempotent() {
  local cfg count
  cfg="$(write_shipped_conf idempotent.yaml)"
  run_clean \
    CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306' \
    CUBE_EXTERNAL_GOLDENDB_MODE=masterslave \
    patch_conf_external_instance_db "${cfg}"
  run_clean \
    CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306' \
    CUBE_EXTERNAL_GOLDENDB_MODE=random \
    patch_conf_external_instance_db "${cfg}"

  count="$(grep -c '^  goldendb:$' "${cfg}")"
  [[ "${count}" -eq 1 ]] || fail "expected one goldendb sub-block, got ${count}"
  grep -Fq '    mode: "random"' "${cfg}" || fail "mode was not updated in place"
}

test_goldendb_patch_noop_without_endpoint() {
  local cfg before
  cfg="$(write_shipped_conf noop.yaml)"
  before="$(cat "${cfg}")"
  run_clean patch_conf_external_instance_db "${cfg}"
  [[ "$(cat "${cfg}")" == "${before}" ]] || fail "no endpoint must leave conf unchanged"
}

test_validation_requires_addrs() {
  if run_clean CUBE_DATABASE_DRIVER=goldendb validate_one_click_database_config 2>/dev/null; then
    fail "goldendb without CUBE_EXTERNAL_GOLDENDB_ADDRS must fail validation"
  fi
}

test_validation_rejects_mixed_engines() {
  if run_clean \
      CUBE_DATABASE_DRIVER=goldendb \
      CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306' \
      CUBE_EXTERNAL_MYSQL_HOST=mysql.example.internal \
      validate_one_click_database_config 2>/dev/null; then
    fail "goldendb + CUBE_EXTERNAL_MYSQL_HOST must fail validation"
  fi
  if run_clean \
      CUBE_DATABASE_DRIVER=mysql \
      CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306' \
      validate_one_click_database_config 2>/dev/null; then
    fail "CUBE_EXTERNAL_GOLDENDB_ADDRS with driver=mysql must fail validation"
  fi
}

test_validation_accepts_goldendb() {
  run_clean \
    CUBE_DATABASE_DRIVER=goldendb \
    CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306,10.0.0.12:3306' \
    validate_one_click_database_config \
    || fail "a consistent goldendb config must pass validation"
}

# The skip-local-MySQL predicate must key off the GoldenDB endpoint too,
# otherwise the bundled MySQL container is started next to a GoldenDB cluster.
test_skip_local_mysql_includes_goldendb() {
  run_clean CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306' one_click_skip_local_mysql \
    || fail "CUBE_EXTERNAL_GOLDENDB_ADDRS must skip the bundled MySQL container"
  if run_clean one_click_skip_local_mysql; then
    fail "no endpoint must not skip the bundled MySQL container"
  fi
}

test_goldendb_patch_keeps_cn_list
test_goldendb_block_removed_on_mysql_switch
test_goldendb_patch_is_idempotent
test_goldendb_patch_noop_without_endpoint
test_validation_requires_addrs
test_validation_rejects_mixed_engines
test_validation_accepts_goldendb
test_skip_local_mysql_includes_goldendb

echo "goldendb one-click tests OK"
