#!/usr/bin/env bash
# dbx-plugins 三插件一键自动化验证（workspace 级编排）。
#
#   shared/test-all.sh                # 全量：单元 + 前端 + 容器化 smoke
#   shared/test-all.sh --quick        # 快速：只跑单元测试与前端三件套
#
# 每插件执行：
#   ssh   backend cargo test → 前端 typecheck/vitest/build → smoke（真机容器，
#         无容器按 SKIP 语义）→ mock UI 走查；
#   ldap  go vet + go test（-cover 覆盖率）→ 前端三件套 → 容器 smoke
#         （smoke_container.py 自编排 OpenLDAP，随机密码仅环境变量）；
#   files cargo test → 前端三件套 → container_smoke.sh（MinIO + OpenSSH）。
#
# 退出码语义：任一插件 FAIL 即非零；SKIP 不算失败（但汇总里如实呈现）。
# 环境要求：cargo/go/pnpm（PATH 自动补全）、docker（容器段缺失时按 SKIP）。
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
QUICK=0
[ "${1:-}" = "--quick" ] && QUICK=1

export PATH="$HOME/.cargo/bin:$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$PATH"

declare -a SUMMARY=()
FAILED_PLUGINS=()

record() { # record <plugin> <status: PASS|SKIP|FAIL> <note>
  SUMMARY+=("$(printf '%-6s %-5s %s' "$1" "$2" "$3")")
  [ "$2" = "FAIL" ] && FAILED_PLUGINS+=("$1")
  return 0
}

run_step() { # run_step <plugin> <label> <cmd...>
  local plugin="$1" label="$2"; shift 2
  echo ""
  echo "======== [$plugin] $label ========"
  if "$@"; then
    echo "-------- [$plugin] $label: PASS"
    return 0
  fi
  echo "-------- [$plugin] $label: FAIL"
  return 1
}

frontend_trio() { # <plugin>
  run_step "$1" "frontend typecheck" pnpm --dir "$ROOT/$1/frontend" typecheck &&
    run_step "$1" "frontend vitest" pnpm --dir "$ROOT/$1/frontend" test &&
    run_step "$1" "frontend build" pnpm --dir "$ROOT/$1/frontend" build
}

# ---------------------------------------------------------------- ssh --------
echo ""
echo "############################################################"
echo "# [ssh] io.dbx.ssh"
echo "############################################################"
SSH_UNIT_OK=0
if run_step ssh "backend cargo test" cargo test --manifest-path "$ROOT/ssh/backend/Cargo.toml"; then
  SSH_UNIT_OK=1
  # smoke 走 release sidecar（工作区未安装新 id 插件包，显式指路，同 ssh test.sh）；
  # 只在命令前缀内联，禁止全局 export（会泄漏进后续插件的 smoke）。
  if [ "$QUICK" = 0 ]; then
    run_step ssh "sidecar release build" cargo build --release --manifest-path "$ROOT/ssh/backend/Cargo.toml"
  fi
  SSH_SIDECAR="$ROOT/ssh/backend/target/release/dbx-plugin-ssh"
fi
if [ "$SSH_UNIT_OK" = 1 ] && frontend_trio ssh; then
  if [ "$QUICK" = 1 ]; then
    record ssh PASS "cargo+vitest 全绿（quick 模式）"
  elif run_step ssh "smoke (real container)" env DBX_PLUGIN_SIDECAR="$SSH_SIDECAR" \
    python3 "$ROOT/ssh/scripts/smoke_test.py" &&
    run_step ssh "mock UI walkthrough" node "$ROOT/ssh/scripts/smoke_ui_mock.mjs"; then
    record ssh PASS "cargo+vitest+smoke+ui-mock 全绿"
  else
    record ssh FAIL "smoke/ui-mock 失败，见上方输出"
  fi
else
  record ssh FAIL "单元/前端失败，见上方输出"
fi

# ---------------------------------------------------------------- ldap -------
echo ""
echo "############################################################"
echo "# [ldap] io.dbx.ldap"
echo "############################################################"
if run_step ldap "backend go vet + test (-cover)" bash -c \
  "cd '$ROOT/ldap/backend' && go vet ./... && go test -cover ./..."; then
  if ! frontend_trio ldap; then
    record ldap FAIL "前端三件套失败，见上方输出"
  elif [ "$QUICK" = 1 ]; then
    record ldap PASS "go+vitest 全绿（quick 模式）"
  else
    # 防跨插件环境泄漏（前一轮 ssh 的 sidecar 指路不能带进来）
    unset DBX_PLUGIN_SIDECAR || true
    # 容器编排：随机一次性管理密码，仅经环境变量传递（不落盘不入库）
    LDAP_ADMIN_PASSWORD="$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    export LDAP_ADMIN_PASSWORD
    if run_step ldap "container orchestration (S1-S10)" \
      python3 "$ROOT/ldap/scripts/smoke_container.py" &&
      run_step ldap "smoke S-suite" python3 "$ROOT/ldap/scripts/smoke_test.py"; then
      record ldap PASS "go+vitest+容器 smoke 全绿"
    elif [ "${LDAP_TEST_REQUIRE:-0}" = "1" ]; then
      record ldap FAIL "容器 smoke 失败（LDAP_TEST_REQUIRE=1）"
    else
      record ldap SKIP "容器 smoke 未跑通（docker/镜像缺失按 SKIP 语义，见输出）"
    fi
  fi
else
  record ldap FAIL "go test 失败，见上方输出"
fi

# ---------------------------------------------------------------- files ------
echo ""
echo "############################################################"
echo "# [files] io.dbx.files"
echo "############################################################"
if run_step files "backend cargo test" cargo test --manifest-path "$ROOT/files/backend/Cargo.toml" &&
  frontend_trio files; then
  if [ "$QUICK" = 1 ]; then
    record files PASS "cargo+vitest 全绿（quick 模式）"
  else
    if [ ! -x "$ROOT/files/backend/target/release/dbx-plugin-files" ]; then
      run_step files "sidecar release build" cargo build --release --manifest-path "$ROOT/files/backend/Cargo.toml"
    fi
    echo ""
    echo "======== [files] container smoke (MinIO+OpenSSH) ========"
    set +e
    DBX_PLUGIN_SIDECAR="$ROOT/files/backend/target/release/dbx-plugin-files" \
      "$ROOT/files/scripts/container_smoke.sh"
    rc=$?
    set -u
    case "$rc" in
      0) record files PASS "cargo+vitest+容器 smoke 全绿（s3+sftp 真机）" ;;
      3) record files SKIP "docker 不可用，容器 smoke 按跳过处理" ;;
      *) record files FAIL "容器 smoke 失败（exit=$rc），见上方输出" ;;
    esac
  fi
else
  record files FAIL "单元/前端失败，见上方输出"
fi

# ---------------------------------------------------------------- 汇总 -------
echo ""
echo "############################################################"
echo "# 汇总"
echo "############################################################"
for line in "${SUMMARY[@]}"; do echo "  $line"; done
if [ "${#FAILED_PLUGINS[@]}" -gt 0 ]; then
  echo "RESULT: FAIL — ${FAILED_PLUGINS[*]}"
  exit 1
fi
echo "RESULT: PASS"
