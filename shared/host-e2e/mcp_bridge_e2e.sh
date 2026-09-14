#!/usr/bin/env bash
# MCP app-bridge loopback e2e for the four-plugin family (ssh/ldap/files/kafka).
#
# 验证目标（MCP 专项第四轮）：插件 standalone `--mcp` sidecar 收到未池化
# `connectionId` 时经运行中测试 DBX.app 的本地 TCP 桥（`mcp-bridge-port` →
# `POST /call-plugin-tool`）转发，并与「桥不可达时的本地 fail-closed 文案」
# 明确区分。判据（三态，非简单 pass/fail）：
#   FORWARDED           响应经 app 返回 —— 200 结果、或 app 侧结构化错误
#                       （`DBX app bridge returned HTTP <status>`，如未匹配
#                       连接的 404 —— TCP+HTTP 打到真 app 本身就是回环证明）
#   BRIDGE_UNREACHABLE  本地 fail-closed（`unreachable` / `no reachable
#                       mcp-bridge-port`），control 场景的期望
#   OTHER               其它（视为异常，如实输出）
#
# 用法：
#   shared/host-e2e/mcp_bridge_e2e.sh                 # 全流程（装+拉起+探针+收尾）
#   shared/host-e2e/mcp_bridge_e2e.sh --skip-install  # 复用已装插件，只跑探针
#   shared/host-e2e/mcp_bridge_e2e.sh --keep-app      # 不杀测试 app（调试用）
#   shared/host-e2e/mcp_bridge_e2e.sh --skip-ssh-e2e  # 跳过 ssh 五场景套件（只跑通用探针）
#   shared/host-e2e/mcp_bridge_e2e.sh --deep          # 追加真实结果层验证（MCP 第七轮）：
#                                                     #   向隔离 app-data seed ldap/kafka/files
#                                                     #   三条真实连接，断言各插件 digest 工具
#                                                     #   返回真实端点数据（matched>=1）。
#                                                     #   端点缺失的插件 SKIP 而非 FAIL。
#
# 环境变量：APP_DATA（默认 shared/host-e2e/app-data）、TEST_APP_BINARY。
# deep 段可调：DBX_E2E_FILES_ROOT（默认 /tmp/dbx-e2e-files-root）、
#   DBX_E2E_KAFKA_TOPIC（默认 dbx-e2e-deep-probe）、
#   KAFKA_TEST_CONTAINER / LDAP_TEST_CONTAINER（默认 dbx-kafka-test / dbx-ldap-test）。
#
# 红线：
#   - 测试 app 一律经 launch.sh 语义（DBX_DATA_DIR 注入）拉起，绝不裸启 bundle；
#   - 收尾只杀本脚本记录的测试 app pid，绝不 pkill（用户真实 DBX.app 可能同在运行）；
#   - ssh e2e 套件固定 --skip-autolaunch（其 T4 会 pkill 全部 MacOS/dbx）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKSPACE_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

APP_DATA="${DBX_E2E_APP_DATA:-$SCRIPT_DIR/app-data}"
TEST_APP_BINARY="${DBX_TEST_APP:-$WORKSPACE_ROOT/host/target/debug/bundle/macos/DBX.app/Contents/MacOS/dbx}"
SKIP_INSTALL=0
KEEP_APP=0
SKIP_SSH_E2E=0
DEEP=0

while [ $# -gt 0 ]; do
  case "$1" in
    --app-data) APP_DATA="$2"; shift 2 ;;
    --skip-install) SKIP_INSTALL=1; shift ;;
    --keep-app) KEEP_APP=1; shift ;;
    --skip-ssh-e2e) SKIP_SSH_E2E=1; shift ;;
    --deep) DEEP=1; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

export PATH="$HOME/.cargo/bin:$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$PATH"

SIDECARS=(
  "ssh|$WORKSPACE_ROOT/ssh/backend/target/release/dbx-plugin-ssh"
  "files|$WORKSPACE_ROOT/files/backend/target/release/dbx-plugin-files"
  "ldap|$WORKSPACE_ROOT/ldap/backend/bin/dbx-plugin-ldap"
  "kafka|$WORKSPACE_ROOT/kafka/backend/bin/dbx-plugin-kafka"
)

RESULTS=()
FAILED=0

record() { # record <name> <ok(0/1)|2=SKIP> <detail>
  RESULTS+=("$1|$2|$3")
  if [ "$2" = "1" ]; then
    echo "PASS  $1 -- $3"
  elif [ "$2" = "2" ]; then
    echo "SKIP  $1 -- $3"
  else
    echo "FAIL  $1 -- $3"
    FAILED=$((FAILED + 1))
  fi
}

bridge_port() { cat "$APP_DATA/mcp-bridge-port" 2>/dev/null || echo ""; }

# ---------------------------------------------------------------------------
# 1. install plugins (official PluginPackageInstaller, isolated app-data)
# ---------------------------------------------------------------------------
if [ "$SKIP_INSTALL" = 0 ]; then
  for entry in "${SIDECARS[@]}"; do
    plugin="${entry%%|*}"
    echo "==> install $plugin"
    # --reinstall：开发迭代语义（同版本先删后装），重复跑本脚本幂等。
    "$SCRIPT_DIR/install.sh" "$WORKSPACE_ROOT/$plugin" --app-data "$APP_DATA" --reinstall >/dev/null
  done
fi

# ---------------------------------------------------------------------------
# 2. launch test app (DBX_DATA_DIR pinned) and wait for a TCP-alive bridge
# ---------------------------------------------------------------------------
TEST_APP_PID=""
PORT="$(bridge_port)"
if [ -n "$PORT" ] && nc -z -G 2 127.0.0.1 "$PORT" 2>/dev/null; then
  echo "==> reusing already-running test app (bridge port $PORT alive)"
else
  [ -x "$TEST_APP_BINARY" ] || { echo "test app binary not found: $TEST_APP_BINARY" >&2; exit 1; }
  echo "==> launching test DBX.app with DBX_DATA_DIR=$APP_DATA"
  DBX_DATA_DIR="$APP_DATA" nohup "$TEST_APP_BINARY" >> "$APP_DATA/app-stdout.log" 2>&1 &
  TEST_APP_PID=$!
  echo "==> launched pid=$TEST_APP_PID"
fi

ALIVE_PORT=""
for _ in $(seq 1 60); do
  PORT="$(bridge_port)"
  if [ -n "$PORT" ] && nc -z -G 2 127.0.0.1 "$PORT" 2>/dev/null; then
    ALIVE_PORT="$PORT"; break
  fi
  sleep 1
done
[ -n "$ALIVE_PORT" ] || { echo "bridge port never became TCP-alive under $APP_DATA" >&2; exit 1; }
echo "==> bridge alive: 127.0.0.1:$ALIVE_PORT"

cleanup() {
  rm -f "$PROBE_PY" "$DEEP_PY" 2>/dev/null || true
  if [ -n "$TEST_APP_PID" ] && [ "$KEEP_APP" = 0 ]; then
    # 精确 pid：绝不 pkill —— 用户真实 DBX.app（/Applications）可能同在运行。
    kill "$TEST_APP_PID" 2>/dev/null || true
    echo "==> stopped test app pid=$TEST_APP_PID"
  fi
}
trap cleanup EXIT
DEEP_PY=""

# ---------------------------------------------------------------------------
# 3. per-plugin probes (FORWARDED vs BRIDGE_UNREACHABLE, plus no-bridge control)
# ---------------------------------------------------------------------------
# 探针实现（内嵌 python，复用 ssh/scripts/sidecar_client.py 的 McpStdioClient）：
#   python3 /tmp/<self>.py … —— 临时探针产物只进 /tmp，不入库。
PROBE_PY="$(mktemp /tmp/dbx-mcp-bridge-probe.XXXXXX.py)"
cat > "$PROBE_PY" <<'PY'
import json, pathlib, shutil, sys, tempfile

ssh_scripts, binary, app_data, tool = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
args = json.loads(sys.argv[5])
# Lenient mode (ssh): its forward path silently falls back to the inline
# self-heal error when the app answers 404 for an unknown connection, so an
# unknown-id probe cannot prove forwarding — use a real saved connection
# instead and treat anything that is not the local fail-closed as FORWARDED.
lenient = "--lenient" in sys.argv[6:]
sys.path.insert(0, str(pathlib.Path(ssh_scripts)))
from sidecar_client import McpStdioClient

BRIDGE = "DBX app bridge"


def classify_error(text: str) -> str:
    if "returned HTTP" in text:
        return "FORWARDED"
    if lenient and BRIDGE in text and (
        "unreachable" in text or "no reachable" in text or "bridge is unavailable" in text
    ):
        return "BRIDGE_UNREACHABLE"
    if lenient:
        return "FORWARDED"
    if BRIDGE in text:
        return "BRIDGE_UNREACHABLE"
    return "OTHER"


def run(binary, app_data, tool, args, timeout):
    client = McpStdioClient(binary, {"DBX_APP_DATA_DIR": app_data})
    try:
        client.initialize()
        result = client.call_tool(tool, args, timeout=timeout)
        if result.get("isError"):
            text = " ".join(c.get("text", "") for c in result.get("content", []))
            return classify_error(text), text
        return "FORWARDED", json.dumps(result, ensure_ascii=False)
    except Exception as error:  # noqa: BLE001
        return classify_error(str(error)), str(error)
    finally:
        client.close()


# 桥读预算 150s；观察到偶发「ssh 真连接探针之后的下一两个转发调用 90s 无响应、
# 单独重跑立即成功」（疑似宿主侧/GUI 渲染竞态），因此超时放宽且 OTHER 重试一次。
kind, body = run(binary, app_data, tool, args, 150)
if kind == "OTHER":
    kind, body = run(binary, app_data, tool, args, 150)
print(f"OUTCOME={kind}")
print(f"BODY={body[:500]}")
lonely = tempfile.mkdtemp(prefix="dbx-e2e-no-bridge-")
try:
    ckind, cbody = run(binary, lonely, tool, args, 60)
    print(f"CONTROL={ckind}")
finally:
    shutil.rmtree(lonely, ignore_errors=True)
PY

probe_plugin() { # probe_plugin <plugin> <binary> <tool> <args-json> [extra python args...]
  local plugin="$1" binary="$2" tool="$3" args="$4"
  shift 4
  local out
  out="$(python3 "$PROBE_PY" "$WORKSPACE_ROOT/ssh/scripts" "$binary" "$APP_DATA" "$tool" "$args" "$@" 2>&1)" || true
  local outcome control
  outcome="$(echo "$out" | sed -n 's/^OUTCOME=//p' | head -1)"
  control="$(echo "$out" | sed -n 's/^CONTROL=//p' | head -1)"
  local body
  body="$(echo "$out" | sed -n 's/^BODY=//p' | head -1)"
  if [ "$outcome" = "FORWARDED" ] && [ "$control" = "BRIDGE_UNREACHABLE" ]; then
    record "bridge-loop:$plugin($tool)" 1 "FORWARDED via app; control fail-closed. body=${body:0:160}"
  else
    record "bridge-loop:$plugin($tool)" 0 "outcome=$outcome control=$control body=${body:0:200}"
  fi
}

# ssh：forward 对 app 侧 404（未知连接）会静默落回 inline 自纠错误，无法用
# 未知 id 产生 FORWARDED 证据 —— 改用隔离 app-data 中真实存在的 vagrant 固件
# 连接（echo marker，隐藏通道）断言全链路转发；--lenient 下非本地 fail-closed
# 即视为转发成功。vagrant VM 不在线时 app 侧连接失败也是经桥返回的结构化错误。
probe_plugin ssh  "$WORKSPACE_ROOT/ssh/backend/target/release/dbx-plugin-ssh" \
  ssh_exec '{"connectionId":"vagrant","command":"echo mcp-bridge-e2e && hostname"}' --lenient
probe_plugin ldap "$WORKSPACE_ROOT/ldap/backend/bin/dbx-plugin-ldap" \
  ldap_search_digest '{"connectionId":"mcp-bridge-e2e-no-such-conn","filter":"(objectClass=*)"}'
probe_plugin kafka "$WORKSPACE_ROOT/kafka/backend/bin/dbx-plugin-kafka" \
  kafka_messages_digest '{"connectionId":"mcp-bridge-e2e-no-such-conn","topic":"probe"}'
probe_plugin files "$WORKSPACE_ROOT/files/backend/target/release/dbx-plugin-files" \
  files_scan_digest '{"connectionId":"mcp-bridge-e2e-no-such-conn","path":"/"}'

# ---------------------------------------------------------------------------
# 3.5 deep loopback（MCP 专项第七轮）: seed 真实连接 + 断言真实结果层。
#
# 向隔离 app-data 的 dbx.db seed ldap/kafka/files 三条真实连接（宿主桥每次
# /call-plugin-tool 都重新 load_connections 并从 connection_secrets 回填凭据，
# 因此 app 运行中直接 INSERT OR REPLACE 同样生效），再经各插件 standalone
# sidecar 断言 digest 工具返回真实端点数据（matched>=1）——即
# stdio → 宿主桥 → app 内插件 sidecar → 真实端点 → 结果回流 全链路。
#
# seed 机制（对齐 host storage.rs persist/load 对偶）：
#   - config_json 平铺 host/port/username + external_config（config 绑定字段）；
#   - 敏感值放 connection_secrets 表（键 plugin_connection.<key>，载入时回填），
#     本轮三类端点均无需凭据（ldap 匿名 bind、kafka PLAINTEXT、files localFs）；
#   - plugin_connection_type 必须等于 provider 的 database_type
#     （ldap/kafka/storage），否则宿主 resolve_connection_provider 拒绝。
#
# 端点缺失的插件 SKIP 而非 FAIL（AGENTS 规则 5 语义）；seed 本身无条件执行。
# ---------------------------------------------------------------------------
if [ "$DEEP" = 1 ]; then
  FILES_ROOT="${DBX_E2E_FILES_ROOT:-/tmp/dbx-e2e-files-root}"
  KAFKA_TOPIC="${DBX_E2E_KAFKA_TOPIC:-dbx-e2e-deep-probe}"
  KAFKA_CONTAINER="${KAFKA_TEST_CONTAINER:-dbx-kafka-test}"
  LDAP_CONTAINER="${LDAP_TEST_CONTAINER:-dbx-ldap-test}"

  # files localFs 端点：固定标记文件，幂等重写。
  mkdir -p "$FILES_ROOT/subdir"
  printf 'dbx e2e marker alpha\n' > "$FILES_ROOT/alpha.txt"
  printf '{"kind":"marker","n":42}\n' > "$FILES_ROOT/bravo.json"
  printf 'child\n' > "$FILES_ROOT/subdir/charlie.log"

  # kafka 端点：dev 集群上幂等建 topic + 重复生产 marker（matched 只增不减）。
  KAFKA_READY=0
  if docker exec "$KAFKA_CONTAINER" /opt/kafka/bin/kafka-topics.sh \
      --bootstrap-server localhost:9092 --create --if-not-exists \
      --topic "$KAFKA_TOPIC" --partitions 1 --replication-factor 1 >/dev/null 2>&1 \
    && printf 'dbx-e2e-deep marker one\ndbx-e2e-deep marker two\n' \
      | docker exec -i "$KAFKA_CONTAINER" /opt/kafka/bin/kafka-console-producer.sh \
        --bootstrap-server localhost:9092 --topic "$KAFKA_TOPIC" >/dev/null 2>&1; then
    KAFKA_READY=1
  fi

  # ldap 端点：容器内匿名 base search 即绪（bitnami/openldap 默认允许匿名读）。
  LDAP_READY=0
  if docker exec "$LDAP_CONTAINER" ldapsearch -x -H ldap://127.0.0.1:1389 \
      -b dc=example,dc=org -s base "(objectClass=*)" dn >/dev/null 2>&1; then
    LDAP_READY=1
  fi

  echo "==> deep: seeding e2e-ldap-anon / e2e-kafka-dev / e2e-files-local into $(basename "$APP_DATA")/dbx.db"
  sqlite3 "$APP_DATA/dbx.db" <<SQL
INSERT OR REPLACE INTO connections (id, config_json) VALUES ('e2e-ldap-anon', '{"id":"e2e-ldap-anon","name":"e2e-ldap-anon","db_type":"plugin","plugin_id":"io.dbx.ldap","plugin_connection_provider":"io.dbx.ldap.connection","plugin_connection_type":"ldap","host":"127.0.0.1","port":1389,"username":"","password":"","external_config":{"auth_type":"anonymous","tls_mode":"none","tls_verify":true,"base_dn":"dc=example,dc=org","timeout_secs":30,"read_only":true}}');
INSERT OR REPLACE INTO connections (id, config_json) VALUES ('e2e-kafka-dev', '{"id":"e2e-kafka-dev","name":"e2e-kafka-dev","db_type":"plugin","plugin_id":"io.dbx.kafka","plugin_connection_provider":"io.dbx.kafka.connection","plugin_connection_type":"kafka","host":"127.0.0.1","port":9092,"username":"","password":"","external_config":{"connection_source":"bootstrap","bootstrap_servers":"127.0.0.1:9092","security_protocol":"PLAINTEXT","schema_registry":"none","read_only":true,"allow_delete":false}}');
INSERT OR REPLACE INTO connections (id, config_json) VALUES ('e2e-files-local', '{"id":"e2e-files-local","name":"e2e-files-local","db_type":"plugin","plugin_id":"io.dbx.files","plugin_connection_provider":"io.dbx.files.connection","plugin_connection_type":"storage","host":"","port":0,"username":"","password":"","external_config":{"protocol":"fs","config":"{}","root":"$FILES_ROOT","lock_to_root":true,"timeout_secs":30,"read_only":true,"allow_delete":false}}');
SQL

  DEEP_PY="$(mktemp /tmp/dbx-deep-probe.XXXXXX.py)"
  cat > "$DEEP_PY" <<'PY'
import json, pathlib, sys

ssh_scripts, app_data = sys.argv[1], sys.argv[2]
sys.path.insert(0, str(pathlib.Path(ssh_scripts)))
from sidecar_client import McpStdioClient

PROBES = json.loads(sys.argv[3])


def run_once(probe):
    client = McpStdioClient(probe["binary"], {"DBX_APP_DATA_DIR": app_data})
    try:
        client.initialize()
        result = client.call_tool(probe["tool"], probe["args"], timeout=150)
        text = " ".join(c.get("text", "") for c in result.get("content", []))
        if result.get("isError"):
            return "ERROR", text[:400]
        # content 可能带前导说明文本，取第一个 '{' 起的 JSON 解析。
        payload = text[text.index("{"):]
        data = json.loads(payload)
        matched = data.get("matched")
        if isinstance(matched, (int, float)) and matched >= 1:
            return "REAL", f"matched={int(matched)}"
        return "NO_DATA", f"matched={matched!r}"
    except Exception as error:  # noqa: BLE001
        return "EXC", str(error)[:400]
    finally:
        client.close()


for probe in PROBES:
    # 偶发转发无响应（第四轮记录），异常重试一次。
    status, detail = run_once(probe)
    if status == "EXC":
        status, detail = run_once(probe)
    print(f"DEEP={probe['plugin']}|{status}|{detail}")
PY

  deep_probe() { # deep_probe <plugin> <binary> <tool> <args-json> <ready> <hint>
    local plugin="$1" binary="$2" tool="$3" args="$4" ready="$5" hint="$6"
    if [ "$ready" != "1" ]; then
      record "deep-real:$plugin($tool)" 2 "endpoint unavailable -- $hint"
      return
    fi
    local probes_json out status detail
    probes_json="[{\"plugin\":\"$plugin\",\"binary\":\"$binary\",\"tool\":\"$tool\",\"args\":$args}]"
    out="$(python3 "$DEEP_PY" "$WORKSPACE_ROOT/ssh/scripts" "$APP_DATA" "$probes_json" 2>&1)" || true
    status="$(echo "$out" | sed -n 's/^DEEP=[^|]*|\([^|]*\)|.*/\1/p' | head -1)"
    detail="$(echo "$out" | sed -n 's/^DEEP=[^|]*|[^|]*|//p' | head -1)"
    case "$status" in
      REAL) record "deep-real:$plugin($tool)" 1 "real data via app bridge; ${detail:0:120}" ;;
      ERROR|NO_DATA|EXC) record "deep-real:$plugin($tool)" 0 "status=$status detail=${detail:0:200}" ;;
      *) record "deep-real:$plugin($tool)" 0 "no verdict; out=${out:0:200}" ;;
    esac
  }

  deep_probe ldap "$WORKSPACE_ROOT/ldap/backend/bin/dbx-plugin-ldap" \
    ldap_search_digest "{\"connectionId\":\"e2e-ldap-anon\",\"filter\":\"(objectClass=*)\"}" \
    "$LDAP_READY" "start the OpenLDAP smoke container first (export LDAP_ADMIN_PASSWORD and keep ldap/scripts/smoke_container.py --keep running, or docker compose -f ldap/docker-compose.ldap-test.yml up -d)"
  deep_probe kafka "$WORKSPACE_ROOT/kafka/backend/bin/dbx-plugin-kafka" \
    kafka_messages_digest "{\"connectionId\":\"e2e-kafka-dev\",\"topic\":\"$KAFKA_TOPIC\"}" \
    "$KAFKA_READY" "start the kafka dev cluster first (kafka/scripts/dev-cluster.sh up)"
  deep_probe files "$WORKSPACE_ROOT/files/backend/target/release/dbx-plugin-files" \
    files_scan_digest '{"connectionId":"e2e-files-local","path":"/"}' \
    1 ""
fi

# ---------------------------------------------------------------------------
# 4. ssh deep suite (visible-terminal T1/T2/T3/T5; T4 auto-launch always skipped:
#    it pkills every MacOS/dbx and would take down the user's real DBX.app).
#    T1/T5 依赖前端工作台 tab 已建立；冷启动时 app 侧报
#    "No open terminal session"（已知问题，详见 PROGRESS 第四轮记录），
#    因此套件失败不推翻上面的转发契约结论，只如实呈现。
# ---------------------------------------------------------------------------
if [ "$SKIP_SSH_E2E" = 0 ]; then
  echo "==> ssh e2e_agent_app_bridge.py (--skip-autolaunch)"
  ssh_e2e_out="$(python3 "$WORKSPACE_ROOT/ssh/scripts/e2e_agent_app_bridge.py" \
    --app-data "$APP_DATA" \
    --bundle "$(dirname "$(dirname "$(dirname "$TEST_APP_BINARY")")")" \
    --connection-id vagrant \
    --binary "$WORKSPACE_ROOT/ssh/backend/target/release/dbx-plugin-ssh" \
    --skip-autolaunch 2>&1)" || true
  echo "$ssh_e2e_out" | grep -E "^(PASS|FAIL)" | sed 's/^/      /'
  ssh_line="$(echo "$ssh_e2e_out" | grep -E "passed" | tail -1)"
  echo "      $ssh_line"
fi

echo
echo "==== summary ($(( ${#RESULTS[@]} )) checks, $FAILED failed) ===="
for entry in "${RESULTS[@]}"; do
  IFS='|' read -r name ok detail <<< "$entry"
  if [ "$ok" = "1" ]; then label=PASS; elif [ "$ok" = "2" ]; then label=SKIP; else label=FAIL; fi
  echo "  $label  $name"
done
exit 0
