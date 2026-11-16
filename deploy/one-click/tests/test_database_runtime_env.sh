#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Tencent. All rights reserved.
#
# Unit tests for the one-click MySQL fallback wiring: start scripts must
# export the CUBE_SANDBOX_MYSQL_* split fields instead of synthesizing a
# DATABASE_URL (an unescaped password breaks URL parsing, issue #1559), and
# persist_one_click_database_runtime_env must percent-encode DATABASE_URL.
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

assert_value() {
  local file="$1" key="$2" expected="$3"
  local actual
  actual="$(read_env_key "${file}" "${key}")"
  [[ "${actual}" == "${expected}" ]] || fail "expected ${key}='${expected}', got '${actual}'"
}

assert_contains() {
  grep -Fq -- "$2" "$1" || fail "expected $1 to contain: $2"
}

# assert_sourced_value loads the env file the way runtime helpers do (set -a;
# source) and compares the resulting shell variable. Needed for values that are
# persisted quoted because they contain shell metacharacters (e.g. ';').
assert_sourced_value() {
  local file="$1" key="$2" expected="$3"
  local actual
  actual="$(
    set -a
    # shellcheck disable=SC1090
    source "${file}"
    set +a
    printf '%s' "${!key-}"
  )"
  [[ "${actual}" == "${expected}" ]] || fail "expected ${key}='${expected}' after sourcing, got '${actual}'"
}

assert_not_contains() {
  if grep -Fq -- "$2" "$1"; then
    fail "expected $1 NOT to contain: $2"
  fi
}

# Run persist in a clean subshell so leftover CUBE_EXTERNAL_* / driver vars
# from other tests cannot flip the local vs external branch.
persist_db_clean() {
  local env_file="$1"
  shift
  (
    unset CUBE_DATABASE_DRIVER \
      CUBE_EXTERNAL_MYSQL_HOST CUBE_EXTERNAL_MYSQL_PORT CUBE_EXTERNAL_MYSQL_USER \
      CUBE_EXTERNAL_MYSQL_PASSWORD CUBE_EXTERNAL_MYSQL_DB \
      CUBE_EXTERNAL_POSTGRES_HOST CUBE_EXTERNAL_POSTGRES_PORT CUBE_EXTERNAL_POSTGRES_USER \
      CUBE_EXTERNAL_POSTGRES_PASSWORD CUBE_EXTERNAL_POSTGRES_DB \
      CUBE_EXTERNAL_GOLDENDB_ADDRS CUBE_EXTERNAL_GOLDENDB_USER \
      CUBE_EXTERNAL_GOLDENDB_PASSWORD CUBE_EXTERNAL_GOLDENDB_DB \
      CUBE_EXTERNAL_GOLDENDB_MODE CUBE_EXTERNAL_GOLDENDB_PROXY_GROUPS \
      CUBE_EXTERNAL_GOLDENDB_MIN_CONNECTION_PROXYS \
      CUBE_SANDBOX_MYSQL_HOST CUBE_SANDBOX_MYSQL_PORT CUBE_SANDBOX_MYSQL_USER \
      CUBE_SANDBOX_MYSQL_PASSWORD CUBE_SANDBOX_MYSQL_DB
    # shellcheck disable=SC1091
    source "${ONE_CLICK_DIR}/lib/common.sh"
    if [[ "$#" -gt 0 ]]; then
      export "$@"
    fi
    persist_one_click_database_runtime_env "${env_file}"
  )
}

test_start_scripts_export_split_fields() {
  local cubeops="${ONE_CLICK_DIR}/scripts/systemd/cubeops-start.sh"
  local cubeapi="${ONE_CLICK_DIR}/scripts/systemd/cube-api-start.sh"
  local up="${ONE_CLICK_DIR}/scripts/one-click/up.sh"
  for script in "${cubeops}" "${cubeapi}" "${up}"; do
    [[ -f "${script}" ]] || fail "missing start script: ${script}"
    # The fallback must not build a URL: an unescaped password breaks it.
    assert_not_contains "${script}" 'mysql://${mysql_user}'
    assert_contains "${script}" 'CUBE_SANDBOX_MYSQL_HOST:-127.0.0.1'
    assert_contains "${script}" 'CUBE_SANDBOX_MYSQL_PORT:-3306'
    assert_contains "${script}" 'CUBE_SANDBOX_MYSQL_USER:-cube'
    assert_contains "${script}" 'CUBE_SANDBOX_MYSQL_PASSWORD:-cube_pass'
    assert_contains "${script}" 'CUBE_SANDBOX_MYSQL_DB:-cube_mvp'
  done
}

test_persist_encodes_external_mysql_password() {
  local env_file="${TMP_DIR}/external-mysql.env"
  : > "${env_file}"

  persist_db_clean "${env_file}" \
    CUBE_EXTERNAL_MYSQL_HOST=10.0.0.21 \
    CUBE_EXTERNAL_MYSQL_PASSWORD='p#a?b/c'

  assert_value "${env_file}" CUBE_DATABASE_DRIVER mysql
  assert_value "${env_file}" CUBE_EXTERNAL_MYSQL_HOST 10.0.0.21
  assert_value "${env_file}" DATABASE_URL \
    'mysql://cube:p%23a%3Fb%2Fc@10.0.0.21:3306/cube_mvp'
}

test_persist_encodes_local_mysql_password() {
  local env_file="${TMP_DIR}/local-mysql.env"
  : > "${env_file}"

  persist_db_clean "${env_file}" CUBE_SANDBOX_MYSQL_PASSWORD='p#a?b/c'

  assert_value "${env_file}" DATABASE_URL \
    'mysql://cube:p%23a%3Fb%2Fc@127.0.0.1:3306/cube_mvp'
}

# GoldenDB persists its CN list as dedicated keys and drops DATABASE_URL: a
# multi-CN address cannot be expressed as a single-host URL, and leaving a stale
# URL behind would point CubeOps back at one CN.
test_persist_goldendb_drops_database_url() {
  local env_file="${TMP_DIR}/goldendb.env"
  printf 'DATABASE_URL=mysql://stale:stale@127.0.0.1:3306/cube_mvp\n' > "${env_file}"

  persist_db_clean "${env_file}" \
    CUBE_DATABASE_DRIVER=goldendb \
    CUBE_EXTERNAL_GOLDENDB_ADDRS='10.0.0.11:3306,10.0.0.12:3306' \
    CUBE_EXTERNAL_GOLDENDB_PASSWORD='p#a?b/c' \
    CUBE_EXTERNAL_GOLDENDB_PROXY_GROUPS='10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306'

  assert_value "${env_file}" CUBE_DATABASE_DRIVER goldendb
  assert_value "${env_file}" CUBE_EXTERNAL_GOLDENDB_ADDRS '10.0.0.11:3306,10.0.0.12:3306'
  assert_value "${env_file}" CUBE_EXTERNAL_GOLDENDB_USER cube
  assert_value "${env_file}" CUBE_EXTERNAL_GOLDENDB_DB cube_mvp
  assert_value "${env_file}" CUBE_EXTERNAL_GOLDENDB_MODE masterslave
  assert_sourced_value "${env_file}" CUBE_EXTERNAL_GOLDENDB_PROXY_GROUPS \
    '10.0.1.1:3306,10.0.1.2:3306;10.1.0.1:3306'
  assert_not_contains "${env_file}" 'DATABASE_URL='
}

# Switching from GoldenDB to MySQL must scrub the GoldenDB markers so a stale
# addrs list cannot keep pointing deployments at the old engine.
test_persist_mysql_clears_goldendb() {
  local env_file="${TMP_DIR}/goldendb-to-mysql.env"
  cat > "${env_file}" <<'EOF'
CUBE_DATABASE_DRIVER=goldendb
CUBE_EXTERNAL_GOLDENDB_ADDRS=10.0.0.11:3306
CUBE_EXTERNAL_GOLDENDB_USER=cube
CUBE_EXTERNAL_GOLDENDB_PASSWORD=secret
CUBE_EXTERNAL_GOLDENDB_DB=cube_mvp
EOF

  persist_db_clean "${env_file}" \
    CUBE_DATABASE_DRIVER=mysql \
    CUBE_EXTERNAL_MYSQL_HOST=10.0.0.21

  assert_value "${env_file}" CUBE_DATABASE_DRIVER mysql
  assert_value "${env_file}" CUBE_EXTERNAL_MYSQL_HOST 10.0.0.21
  assert_not_contains "${env_file}" 'CUBE_EXTERNAL_GOLDENDB_ADDRS='
  assert_not_contains "${env_file}" 'CUBE_EXTERNAL_GOLDENDB_PASSWORD='
}

test_start_scripts_export_split_fields
test_persist_encodes_external_mysql_password
test_persist_encodes_local_mysql_password
test_persist_goldendb_drops_database_url
test_persist_mysql_clears_goldendb

echo "database runtime env tests OK"
