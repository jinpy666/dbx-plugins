#!/usr/bin/env bash
# Launch a test DBX.app bound to an ISOLATED app-data directory.
#
# 用法：
#   shared/host-e2e/launch.sh                  # 默认隔离 app-data + host debug bundle
#   shared/host-e2e/launch.sh --app-data <dir>
#   shared/host-e2e/launch.sh --app <bundle>   # 指定 DBX.app（默认 host worktree debug bundle）
#
# 关键：宿主数据目录由 DBX_DATA_DIR 环境变量决定。`open` 无法传环境变量，
# 必须直接执行 bundle 内二进制。不带 DBX_DATA_DIR 启动 debug bundle 会读到
# 用户真实数据目录（真实连接全量可见），绝对禁止——本脚本的唯一职责就是钉死这一点。
#
# 前置：插件已通过 install.sh 装进同一 app-data；宿主 debug bundle 已构建
# （host/ 下 pnpm tauri build --debug；宿主上游依赖可能要求 rustc>=1.94，
# 用 RUSTUP_TOOLCHAIN=1.97.1 覆盖，不影响插件族的 1.88.0）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKSPACE_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

APP_DATA="${DBX_E2E_APP_DATA:-$SCRIPT_DIR/app-data}"
TEST_APP="${DBX_TEST_APP:-$WORKSPACE_ROOT/host/target/debug/bundle/macos/DBX.app/Contents/MacOS/dbx}"

while [ $# -gt 0 ]; do
  case "$1" in
    --app-data) APP_DATA="$2"; shift 2 ;;
    --app) TEST_APP="$2"; shift 2 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) echo "unexpected argument: $1" >&2; exit 2 ;;
  esac
done

[ -x "$TEST_APP" ] || { echo "test app binary not found: ${TEST_APP}（先构建宿主 debug bundle）" >&2; exit 1; }
mkdir -p "$APP_DATA"

echo "==> launching test DBX.app with DBX_DATA_DIR=$APP_DATA"
echo "    binary: $TEST_APP"
DBX_DATA_DIR="$APP_DATA" nohup "$TEST_APP" > "$APP_DATA/app-stdout.log" 2>&1 &
echo "==> launched pid=$! （日志: $APP_DATA/app-stdout.log）"
echo "    校验要点：左栏应只见隔离 app-data 中的连接（如 e2e 固件 vagrant），"
echo "    若看到真实连接列表立即退出——说明 DBX_DATA_DIR 未生效。"
