# Skill: dbx-mcp-usage
# DBX 插件 MCP（files / ldap / kafka）agent 使用指引

把 io.dbx.files / io.dbx.ldap / io.dbx.kafka 三个 sidecar 以独立 stdio
MCP 服务器（`--mcp`）接入 ZCode 后，面向 agent 的调用指引：服务器配置、
连接方式、翻页/两阶段写语义、常见坑。协议细节以各插件
`docs/MCP.zh-CN.md` 为权威；**测试与回归方案（分层矩阵、缺口登记、ZCode
真机接入清单）见 `shared/TEST_PLAN_MCP_ZCODE.zh-CN.md`**；**分插件的
任务→工具快速定位**见同目录 `dbx-files-mcp` / `dbx-ldap-mcp` /
`dbx-kafka-mcp`；本文只讲「怎么用、怎么避坑」。

## 服务器配置（ZCode 接入）

用户级 `~/.zcode/cli/config.json` → `mcp.servers`（四插件同构，ssh 已有
`dbx-ssh` 条目可参照）：

```json
{
  "dbx-files": { "type": "stdio", "command": "<仓库>/files/backend/target/release/dbx-plugin-files", "args": ["--mcp"], "env": { "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.files" } },
  "dbx-ldap":  { "type": "stdio", "command": "<仓库>/ldap/backend/bin/dbx-plugin-ldap",           "args": ["--mcp"], "env": { "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.ldap" } },
  "dbx-kafka": { "type": "stdio", "command": "<仓库>/kafka/backend/bin/dbx-plugin-kafka",         "args": ["--mcp"], "env": { "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.kafka" } }
}
```

- **改配置或重建二进制后必须重启会话/MCP 连接**——stdio 进程是会话启动时
  拉起的，旧进程不会热加载新二进制。
- `DBX_PLUGIN_DATA_DIR` 隔离 mcp-settings.json 与 audit.jsonl，不与真实
  DBX 应用数据混用。
- 工具名在 ZCode 里是 `mcp__dbx-files__files_scan_digest` 这类前缀形状；
  服务器内名如 `files_scan_digest`。

## 通用调用语义（三插件一致）

- 协议 MCP `2024-11-05`；先 `initialize` → `notifications/initialized` →
  `tools/list` → `tools/call`。
- **工具级错误以 `isError:true` 的 content 文本返回**（不是 JSON-RPC 协议
  错误；ldap/kafka/files 已对齐；旧版 files/ssh 可能为 -32000 协议错误）。
  错误文本是写给 agent 的可行动指引——照着做（换参数/换工具/换模式），
  不要原样重试。
- **响应上限 16 KiB**：超出截断并置 `truncated:true`。别硬拉全量，改用
  rows 格式 + cursor 翻页或收窄过滤条件。
- **UI 类工具（`*_ui_*`，除 `files_ui_quick_paths`）在 stdio 下固定
  UNAVAILABLE**：它们驱动 DBX 工作台前端。独立模式改用 digest/cursor 族，
  或经 DBX MCP 桥（工作台模式）调用。
- **两阶段写**：删除类操作第一次调用（无 confirmToken）只返回 preview +
  一次性 confirmToken（60s TTL、绑定参数 hash）→ 原参数 + token 重发才
  执行。**改任何一个参数都会使 token 作废**，需重新 preview。
- **cursor 翻页**：digest 返回 `cursorId` → `*_cursor_next {cursorId, n}`
  （n≤20）直到 `done:true`。会话 TTL 10 分钟、LRU 8 个。

## 连接方式（关键差异！）

| 方式 | files | ldap | kafka |
| --- | --- | --- | --- |
| 内联凭据 | `connection: {protocol, root, …}` 对象 | 顶层参数 `host`/`bindDn`/`password`… | 顶层参数 `brokers`… |
| 池化 id | `mcp-inline-<hash>` | `mcp-<hash>` | `mcp-<hash>` |
| 内置本机 | `connectionId: "__local__"` | — | — |
| 写默认门 | 可写 | 可写 | **只读（默认 readOnly:true）** |

- **kafka 写被拒 "read-only" 时：内联参数补 `readOnly: false` 重发**；
  删除类（topics_delete / groups_offsets_reset / topics_records_clear）
  还需 `allowDelete: true`。拒绝消息现在会点名出路。
- 内联凭据不落盘（进程内存池化 + 参数 hash），但会进工具参数与 LLM
  上下文——仅限本机可信会话；生产走工作台保存连接 + DBX MCP 桥
  （凭据由宿主解析转发，引用保存连接的 `connectionId` 会自动桥转发）。
- files 的 stdio tools/list 声明了内联 `connection` 的全部 camelCase 键；
  严格校验的 MCP 宿主也不会丢参。`{"protocol":"local","root":"/tmp/x"}`
  是免凭据本机路径，冒烟首选。

## 各插件速查

### files（12 工具）
- 读：`files_scan_digest`（`format:"rows"` 出 locator 行）→
  `files_cursor_next`；`files_ui_quick_paths` 纯元发现（stdio 可用）。
- 写：`files_write`（base64，标准 RFC4648，空串建空文件；建议 ≤1 MiB、
  硬上限 4 MiB）、`files_mkdir`、单文件 `files_rename`（**目录 rename
  stdio 不可用**）；`files_delete`/`files_purge` 两阶段；purge 拒绝根路径。
- 缺连接参数时报错会附内联写法指引；`__local__` 最快。

### ldap（8 工具）
- 读：`ldap_search_digest`（filter 服务端执行 + 本地聚合，`groupBy`/
  `topN`/`distinct`；`format:"rows"` + cursor 翻页）、`ldap_ui_schema`
  （objectClass/attributeType 名单，校验 DN/属性前先查）。
- 写：`ldap_entry_write`，`add`/`modify` 直执行；`delete`（含子树）两阶段。
- 内联：`host` 必填；`authType` simple（默认）/anonymous/unauthenticated；
  **Kerberos 族与 SASL 细化 stdio 不支持**，需工作台保存连接。
- 只读连接（`readOnly:true`）会在 tools/list 里剔除写工具并附原因。

### kafka（11 工具）
- 读：`kafka_messages_digest`（`topic` **必填**；`offsetStrategy` 默认
  earliest；`filter`/`fieldFilters`/`fields` 投影聚合）→ `kafka_cursor_next`。
- **stdio 没有 topic 列表能力**（`kafka_ui_topics` 仅工作台）：topic 名要
  从用户或集群文档拿；不存在 topic 的报错会点名这一点。
- 写：`kafka_messages_produce` 单阶段（需 `readOnly:false`）；
  `kafka_topics_delete` / `kafka_groups_offsets_reset` /
  `kafka_topics_records_clear` 两阶段（需 `readOnly:false` +
  `allowDelete:true`）。
- 内联：`brokers` 必填（数组或逗号分隔串）。

## 常见坑速查（真机 agent 实测沉淀）

| 症状 | 处置 |
| --- | --- |
| kafka produce/删除报 "read-only; write tools are refused" | 内联参数补 `"readOnly": false`（删除类再加 `"allowDelete": true`）重发 |
| kafka 报 topic 不存在 | stdio 无法列 topic——向用户要准确 topic 名或查集群文档，别猜 |
| files_write 报 Invalid base64 | 发标准 RFC4648 base64（无 data: 前缀、无换行） |
| 调 `*_ui_*` 返回 UNAVAILABLE | 独立 stdio 模式无工作台：改 digest 族，或经 DBX MCP 桥（需 DBX 应用在跑） |
| 响应里 `truncated:true` | 收窄过滤/减 topN，或 `format:"rows"` + cursor 翻页 |
| confirmToken 用不了 | 60s TTL 已过或参数变过——重新 preview 拿新 token，再原参数重发 |
| cursor 失效 | TTL 10 分钟或 LRU 被挤掉——重新 digest |
| 改了 sidecar 代码但工具行为没变 | ZCode 的 MCP 进程还是旧二进制：重建后重启会话/重连 MCP |
|ldap 报 Invalid Credentials | 检查 bindDn/password；错误只出结果码不出凭据 |
| **zcode 里整个服务器没出现（配置/二进制都正常）** | 查 `~/.zcode/cli/log/zcode-*.jsonl` 搜 `Invalid result for tools/list`——严格宿主校验拒收整个服务器。已钉过的坑：Go 端 schema 里 nil 切片序列化成 `"required":null` 必被拒（`required` 空时必须省略键，2026-09-14 已修 ldap/kafka）；新 schema 形状先用 strict-shape 检查过一遍再接入 |
| zcode 内调 MCP 工具报 "MCP tool returned an error" | 这就是 sidecar 的 isError content 原文（UNAVAILABLE/参数指引），照文本行动即可，不要原样重试 |

## 测试与验证入口

```bash
# 每插件全场景 MCP smoke（无依赖场景自动跑，容器场景无容器 SKIP）
cd files && DBX_PLUGIN_SIDECAR=backend/target/release/dbx-plugin-files python3 scripts/smoke_mcp.py
cd ldap  && python3 scripts/smoke_mcp.py            # 容器场景需 OpenLDAP 测试容器
cd kafka && python3 scripts/smoke_mcp.py            # 容器场景需 dev 集群

# 族级 schema↔行为 + 严格宿主形状核对（sidecar 任何 MCP 面改动后必跑）
python3 shared/mcp_schema_check.py --binary <sidecar> --plugin io.dbx.<域>

# 测试环境
LDAP_ADMIN_PASSWORD=<随机生成> docker compose -f ldap/docker-compose.ldap-test.yml up -d   # 127.0.0.1:1389
bash kafka/scripts/dev-cluster.sh up                                                        # 127.0.0.1:9092 PLAINTEXT
# files 本机场景零依赖：__local__ 或 {"protocol":"local","root":"…"}
```

手工单发冒烟：
`echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | <sidecar> --mcp`
