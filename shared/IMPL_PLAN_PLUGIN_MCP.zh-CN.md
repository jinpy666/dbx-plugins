# 四插件 MCP 辅助定位设计（ldap / files / kafka 落地，ssh 为基线）

状态：**已全部落地（2026-09-12）**——UI intent + digest/cursor + 两阶段写 +
standalone `--mcp` stdio 模式；桌面/stdio 面真机验收与暴露路径结论见
`shared/PROGRESS-HOST-SUBREPO.zh-CN.md` §27。
范围：为 `io.dbx.ldap`、`io.dbx.files`、`io.dbx.kafka` 设计 MCP 工具面；
`io.dbx.ssh` 既有 MCP（`ssh/docs/MCP.zh-CN.md` + `ssh/backend/src/mcp.rs`，
22 工具）是协议基线，不重做。

## 0. 核心命题

三插件的共同特点是**读侧数据量大**（LDAP 全目录、Kafka 千万级消息、Files
大目录树），而**写侧数据量小**（单条 entry、单条消息、单个文件）。
据此 MCP 工具面按"读分权、写轻量"设计：

1. **读：UI 优先 + 强本地化**。大结果集不走 MCP——AI 通过 UI intent 把条件
   填进工作台触发搜索（用户可视、可继续操作）；AI 直查路径必须**在 sidecar
   本地完成过滤、聚合、翻页**，只把消化后的摘要交给 LLM（见 §3），
   扫描过程数据一条都不出 sidecar。
2. **写：入 MCP 面，配两阶段确认**。写操作 payload 小（单 entry/单消息/
   单文件），适合 MCP 直接完成；安全靠"预览 → confirmToken"两阶段确认 +
   审计 + 连接只读策略（见 §4），而不是靠把写挡在面外。变更类 UI 确认流
   保留不变，MCP 写路径是并行能力。
3. **token 经济是硬指标**：默认 `format: "digest"`（计数 + 分布 + 样本），
   显式要 `rows` 才出行且受 clamp；cursor 翻页由 sidecar 会话承接，
   AI 不重发条件、不重复扫描。

## 1. 架构：UI Intent 通道

MCP 桥（宿主 `dbx_call_plugin_tool`）只到 sidecar，前端不在该路径上。
sidecar→前端已有 Emitter 事件通道（`ldap/audit`、`kafka/stream/messages`
同款机制），UI 驱动走这条通道：

```
MCP 客户端(AI)
   │ dbx_call_plugin_tool { pluginId: "io.dbx.ldap", tool: "ldap_ui_search",
   │                        arguments: { baseDn, filter, scope, attributes } }
   ▼
sidecar mcp/call
   │ 1. 参数校验 → 生成 intentId → intent 状态表登记 {status: pending}
   │ 2. emitter.Event("ldap/ui/intent", {intentId, action:"search", params})
   │ 3. 等待前端 report（默认 5s，mcp/settings/set 可调）
   ▼
前端（已有事件监听模式，新增 useUiIntent 订阅）
   │ 1. 切到搜索面板，SearchForm 填入条件（复用 preset 结构，UI 可视可撤销）
   │ 2. 触发搜索 —— 走既有 前端→sidecar 查询管线，结果进 ResultTable
   │ 3. 调新方法 <domain>/ui/state/report {intentId, status:"applied",
   │       summary:{count, truncated, rows[≤5 截断], anchor}}
   ▼
sidecar 更新 intent 状态表，mcp/call 返回
   { intentId, state:"applied", summary:{...} }
   （超时则返回 {intentId, state:"pending", hint:"工作台未打开或前端未响应"}）
```

配套两件小事：

- **intent 状态表**：sidecar 进程内 `map[intentId]{params, status, summary,
  expiry}`（TTL 60s，只留最近 20 条）。使 AI 可以在返回 pending 后用
  `<x>_ui_state {intentId}` 补查，也支持"刚才那次搜索结果如何"的追问。
- **UI 快照**：前端在关键动作后（面板切换、搜索完成、选中行变化）主动
  `ui/state/report`（无 intentId 的快照型 report），sidecar 缓存最新快照；
  `<x>_ui_state` 不带 intentId 时返回快照 —— 这就是"元素定位"的数据源
  （当前面板、表单值、结果计数、选中行关键字段）。

## 2. 工具面分层

命名沿用 ssh 的 `<域>_<动作>` 工具族（`ssh_exec` / `sftp_ls` 同构）。
`<x>` ∈ {ldap, files, kafka}。

| 层 | 工具 | 说明 |
| --- | --- | --- |
| UI 驱动 | `<x>_ui_focus` | 打开/聚焦指定面板（ldap: search/tree/schema；files: browse/transfers/audit；kafka: messages/topics/groups/schemas） |
| UI 驱动 | `<x>_ui_search` | **核心**：填条件并触发搜索，结果留 UI，回传摘要（见 §1） |
| UI 驱动 | `<x>_ui_select` | 定位元素：ldap 按 DN、kafka 按 partition+offset、files 按 path；命中行高亮并回传该行关键字段摘要 |
| UI 驱动 | `<x>_ui_state` | 读 intent 结果或最新 UI 快照（面板、条件、计数、选中元素） |
| 本地读 | `<x>_search_digest` | **核心**：条件在 sidecar/远端执行 + 本地聚合，默认只出 digest（§3） |
| 本地读 | `<x>_cursor_next` | digest 会话翻页：只取定位字段行，不重发条件不重扫（§3） |
| 元发现 | `ldap_ui_schema` | objectClass/attributeType 清单（schema 缓存已有）——帮 AI 构造合法 filter |
| 元发现 | `kafka_ui_topics` / `files_ui_quick_paths` | topic 列表 / quickPaths（limit 硬上限 50，纯定位用） |
| 写 | `<x>_entry_write`（ldap）/ `<x>_messages_produce`（kafka）/ `<x>_write` 族（files） | 见 §4：预览 → confirmToken 两阶段；破坏性操作强制走两阶段 |
| 骨架 | `mcp/tools`、`mcp/call`、`mcp/settings/get\|set` | ssh 同构骨架；settings 可调 digest 参数、截断宽度、report 等待时长 |

## 3. 本地化处理与 token 经济（读路径）

原则：**过滤、聚合、物化、翻页全部在 sidecar 本地完成；LLM 只见结论**。
四个通用原语，作为 `*_search_digest` 工具的统一参数形状（三插件同构，
对齐"同族参数一致"硬性规则）：

1. **filter（服务端执行）**：复用各域已有过滤——LDAP RFC 4515 filter、
   kafka consume filter 通道（key/value/header）、files glob + size/mtime
   谓词。扫描到的每条记录全文都留在 sidecar，**单条不外溢**。
2. **project（投影）**：只出定位字段（DN / path / topic-partition-offset）
   + 显式请求的属性字段；ldap 走 attributes 下推，kafka 走 schema registry
   解码后字段级投影（JSON path），files 走 stat 元字段选择。
3. **aggregate（本地聚合）**：sidecar 对全量命中做：
   `count`（总数，ldap 复用 sizeLimit 上限语义）、`groupBy`（按 objectClass /
   消息 key / 文件扩展名分组，组数 clamp ≤20）、`distinct`（指定属性值域
   统计）、`topN`（最大文件、最新消息等排序取样 ≤10）、`histogram`（kafka
   按时间桶 / offset 段分布，桶数 ≤12）。聚合结果替代数据本体成为 MCP 返回。
4. **cursor（sidecar 会话翻页）**：`*_search_digest` 成功后物化定位字段
   （上限 1 万条，TTL 10 分钟，进程内 LRU ≤8 会话），返回 `cursorId`；
   AI 用 `<x>_cursor_next {cursorId, n≤20}` 分批取行——**条件不重发、
   远端不重扫、每批只有定位字段 + 请求投影**。cursor 过期返回明确错误 +
   建议"重发 digest"。

返回体规则（硬上限，对齐 ssh `maxUploadBytes` 的 `mcp/settings/set` 可调模式）：

- 默认 `format: "digest"`：`{matched, scanned, stats:{...}, sample:[≤5 行
  截断], cursorId}`。要 `format: "rows"` 才出行，clamp ≤20 行/次。
- 每单元格截断 120 字符；**定位字段不截断**（DN / path / partition-offset）。
- 二进制与大字段不出 MCP（ldap `jpegPhoto`、kafka 大 value 原文、files 文件
  内容正文）：返回占位符 + 定位指引（"该属性在 DN=... 上，已可在 UI 高亮"）；
  files 文本预览上限 64 KiB 且按行截断（远小于工作台 2 MiB read 上限）。
- 单工具响应上限 16 KiB，超出即截断并置 `truncated: true`。

各插件 digest 形态：

- **LDAP `ldap_search_digest`**：filter 在 LDAP 服务端执行（本来就是服务端
  过滤），sidecar 聚合 objectClass 分布、指定 attr 的 distinct 值、按 OU
  子树计数；`ldap_cursor_next` 分批取 DN + 投影属性。
- **Kafka `kafka_messages_digest`**：复用 consume 的 `maxScanRecords` 扫描
  语义（扫描上限已有），本地产出 per-partition 计数、key groupBy、时间
  直方图、（schema 解码后）指定字段 distinct/topN；MCP **不订阅 stream**，
  stream 由用户在 UI 操作。
- **Files `files_scan_digest`**：递归扫描（depth/总数上限，默认 depth 8 /
  10 万条 clamp）+ glob/size/mtime 过滤 → 按扩展名分组、topN 最大/最新
  文件、总大小；`files_cursor_next` 分批取 path。

## 4. 写路径与安全（两阶段确认）

写 payload 小，入 MCP 面；安全机制而非面级封禁。参照 ssh
`mcp_safety.rs` 危险参数识别与 `ssh/docs/MCP.zh-CN.md`「生产环境误操作
防范」，统一两阶段：

```
第一次调用  <x>_entry_write {action:"delete", dn:"...", }        （无 confirmToken）
  → sidecar 返回 {preview:{...变更摘要...}, confirmToken:"c-1a2b",
                  expiresAt}（不执行任何写）
第二次调用  同参数 + confirmToken 且参数 hash 一致 → 执行 + 审计
  （confirmToken 一次性、60s TTL；参数被改即作废重开预览）
```

分级：

- **单阶段直执行**（非破坏、可逆）：ldap `add/modify`、kafka
  `messages/produce`（单条小消息）、files `write/mkdir/rename`。照常审计
  `source:"mcp"`。
- **强制两阶段**：ldap `delete`（含 recursive）、`modifyDn`；kafka
  `topics/delete`、`groups/offsets/reset`、`topics/records/clear`；files
  `delete/purge`。purge 沿用"拒绝根路径"红线。
- **连接只读策略**：连接配置为只读（ldap 既有 ensure_writable/DN 白名单
  语义、files 只读协议）时，写类工具**不进 `mcp/tools` 清单**（而非注册了
  再报错），并在工具描述中说明原因——AI 不会白白尝试。
- **审计**：所有写操作照各插件 audit 基线记 `source:"mcp"`，M0 审计事件
  形状不变。
- Scoped AI 会话由宿主既有行为兜底：禁 `dbx_call_plugin_tool`。

## 5. 降级矩阵

| 场景 | `*_ui_*` | `*_search_digest` / cursor | 写工具 |
| --- | --- | --- | --- |
| 工作台打开、前端在线 | 正常（applied + summary） | 可用 | 可用 |
| 工作台打开但面板无该 action | `rejected` + reason | 可用 | 可用 |
| 工作台未打开 / 前端未响应 | `pending` → 超时 hint（引导走 digest） | 可用 | 可用 |
| 独立 stdio `--mcp` | 明确 `UNAVAILABLE`（"此工具需要 DBX 工作台"），不假死 | 可用 | 可用（凭据内联/桥接兜底，同 ssh 模式） |
| 连接只读策略 | 不受影响 | 可用 | 不注册进工具清单 |
| Scoped AI 会话 | 宿主禁 `dbx_call_plugin_tool` | 同左 | 同左 |

## 6. 各插件落地要点

### 6.1 ldap（先行，通道验证样板）

- 事件 `ldap/ui/intent`、汇报 `ldap/ui/state/report`；前端 handler 挂
  `SearchForm.vue`（FilterGroup 整体替换走既有 preset 反序列化路径），
  结果锚点 = `ResultTable.vue` 行 DN。
- digest：filter 下推 LDAP 服务端，本地聚合 objectClass/attr 分布；
  cursor 物化 DN 列表。
- 写：`ldap_entry_write`（action: add/modify/delete/modifyDn），delete
  强制两阶段，recursive 删除沿用子树 1000 上限。
- `ldap_ui_schema` 读 sidecar schema 缓存；audit 记 `source:"mcp"`。

### 6.2 files

- 事件 `files/ui/intent`；handler 挂 `PathField.vue`/`FileTable.vue`
  （导航 = 填 path + 触发 listPaged），锚点 = 行 path。
- digest：`files_scan_digest` 递归 + 谓词过滤 + 扩展名分组/topN；
  cursor 分批取 path。注意 files 无 `ui_test.mjs`，用 vitest（mockDbxHost
  镜像新事件）+ shared/host-e2e 真机复验。
- 写：`files_write/mkdir/rename` 单阶段；`delete/purge` 两阶段；
  `files_write` 沿用 dataBase64 ≤4MiB（MCP 侧建议 ≤1 MiB，超出提示走
  工作台/传输）。

### 6.3 kafka

- 事件 `kafka/ui/intent`；handler 挂 `MessagesPanel.vue`（consume 表单：
  topic/offsetStrategy/limit/filter 各通道），锚点 = partition+offset；
  `kafka_ui_state` 额外报 stream 状态。
- digest：复用 `maxScanRecords` 扫描上限，本地 per-partition 计数、key
  groupBy、时间直方图、schema 解码后字段投影聚合。
- 写：`kafka_messages_produce` 单阶段（单条小消息）；`topics/delete`、
  `groups/offsets/reset`、`topics/records/clear` 两阶段。
- kafka 是三插件中唯一无 MCP 规划段落的，本设计补入其 IMPL_PLAN/PROTOCOL。

## 7. 公共实现点（单点维护，硬性规则 7）

- `shared/frontend/` 新增 `useUiIntent(domain, handlers)`：intent 事件订阅
  + report 调用的公共 composable（事件形状归一化沿用既有 binaryEvent 模式）；
  三插件相对引用，不各抄一份。
- **两阶段确认与 digest/cursor 状态机是纯逻辑，Go/Rust 两份实现但测试用例
  同表**（验收用例清单已沉淀 `shared/MCP_ACCEPTANCE.zh-CN.md`，2026-09-13
  三轮专项后为权威契约表，防形状漂移）；`mcp/call` 骨架照 ssh
  mcp.rs 移植（Go × 2、Rust × 1）。
- 各插件 `mockDbxHost`/`mockHost` 必须镜像新事件/方法形状（防单测脱节），
  同步 `env.d.ts` 类型。
- 协议文档同步：三插件 docs 协议章节各加 `ui/intent` 事件、`ui/state/report`
  方法、digest/cursor 工具参数表、写工具两阶段语义（硬性规则 2）。
- 七语文案：intent 状态提示、UNAVAILABLE 错误、confirmToken 过期提示、
  digest 截断提示等。

## 8. 里程碑与验收

| 里程碑 | 内容 | 验收 |
| --- | --- | --- |
| M1 ldap | sidecar `mcp/*` 骨架 + intent 通道 + `useUiIntent` + `ldap_ui_*` + `ldap_search_digest`/cursor + `ldap_entry_write` 两阶段 | `smoke_mcp.py`（未注册 SKIP）、ui_test.mjs intent 用例、digest/cursor/confirmToken 单测、只读连接不注册写工具用例 |
| M2 files | `files_ui_*` + `files_scan_digest`/cursor + 写族 | smoke + host-e2e 真机 |
| M3 kafka | `kafka_ui_*` + `kafka_messages_digest` + 写族 + 协议文档补 MCP 段 | smoke + ui_test.mjs |
| M4 收尾 | 三插件 PROGRESS/对标更新、七语复查、ssh MCP 文档加"兄弟插件沿用"交叉引用 | 完成定义四件套 |
