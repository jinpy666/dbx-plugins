# MCP 集成（M3）

kafka 插件的 MCP 工具面（设计来源 `shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md`
v2，ssh `mcp.rs` 骨架 → ldap Go 版 → kafka 同构移植）。实现在 sidecar
`internal/mcp/`（Go），经 DBX MCP 桥（`dbx_list_plugin_tools` /
`dbx_call_plugin_tool`）调用插件协议方法 `mcp/tools`、`mcp/call`、
`mcp/settings/get|set`。桥按 `connectionId` 转发标准 lifecycle payload
（`mcp/call` 的 `lifecycle` 字段），凭据由宿主解析，**工具参数里不出现
任何密码**。

核心设计（详见设计文档）：

1. **读：UI 优先 + 强本地化**——千万级消息量不进 MCP；AI 用 UI intent 把
   consume 条件填进消息面板（用户可视可继续操作），或用 digest 在 sidecar
   本地聚合 + cursor 翻页。**MCP 不订阅 stream**（stream 由用户在 UI 操作）；
   digest 复用 consume 的 `maxScanRecords` 扫描语义与 filter 各通道，
   扫描过程数据一条不出 sidecar。
2. **写：入 MCP 面 + 两阶段确认**——`kafka_messages_produce`（单条小消息）
   单阶段直执行；`kafka_topics_delete`、`kafka_groups_offsets_reset`、
   `kafka_topics_records_clear` 强制 preview → confirmToken。
3. **token 经济**——默认 `format:"digest"`（计数 + 分布 + 样本），显式要
   `rows` 才出行且 clamp ≤20 行；单响应上限 16 KiB；大 value 原文不出
   MCP（占位符 + partition/offset 定位指引）。

## 事件：`kafka/ui/intent`（sidecar → 前端）

sidecar 收到 UI 驱动类工具调用时：生成 `intentId` → intent 状态表登记
（进程内 map，TTL 60s，LRU 20 条，`pending`）→ 发本事件 → 等待前端
report（默认 5s，`mcp/settings/set` 的 `reportWaitMs` 可调）。

```json
{ "intentId": "i-1a2b3c4d", "action": "search",
  "params": { "topic": "orders", "offsetStrategy": "earliest", "limit": 100,
              "valueFilter": "paid", "matchMode": "contains",
              "connectionId": "…" } }
```

| action | params | 前端行为 |
| --- | --- | --- |
| `search` | `topic` 必填；`offsetStrategy?`、`limit?`、`filter?`、`keyFilter?`、`valueFilter?`、`headerFilter?`、`matchMode?`、`groupId?`、`partitions?[]`、`offsetTime?` | 消息面板 consume 表单填条件（App 先切选中 topic）→ 触发消费 → 结果进消息表 |
| `focus` | `panel`（messages \| topics \| groups \| schemas） | 切换/聚焦面板 |
| `select` | `partition`、`offset` 必填；`topic?` | 当前消息结果中按 partition+offset 定位命中行并打开详情抽屉 |

前端消费统一走 `shared/frontend/uiIntent.ts` 的 `useUiIntent("kafka",
handlers)`（公共层单点维护，插件不各抄一份）；落表/定位在
`MessagesPanel.vue` 的 `applyIntentConsume` / `applyIntentSelect`（经
defineExpose 由 App 装配）。

## 方法：`kafka/ui/state/report`（前端 → sidecar）

- **intent 回报**：`{intentId, status: "applied"|"rejected", summary,
  reason?}`。summary = `{count, truncated?, rows[≤5], anchor?, reason?}`，
  每 cell 截 120 字符（`cellWidth` 可调），**partition+offset 定位字段
  不截断**（anchor 形如 `orders-p0-o42`）。
- **快照型**：无 `intentId`、`status:"snapshot"`——前端在关键动作后
  （面板切换、topic 选中、消费完成）主动上报 `{panel, topic?, count?,
  anchor?}`，sidecar 缓存最新快照；`kafka_ui_state` 不带 intentId 时返回。
- 未知/已过期 intentId 回报报业务错误（-32000）。

## 工具一览（11 个）

`mcp/tools` 可带可选 `{connectionId}`：该连接配置为只读时全部 4 个写工具
**不进清单**；`allow_delete=false` 时仅剔除删除类两工具（topics/delete、
topics/records/clear），并附 `omittedWriteTools` 原因说明；未带
connectionId 时全量列出（调用时仍有策略门拒绝，纵深防御）。Scoped AI
会话由宿主禁 `dbx_call_plugin_tool`。

| 工具 | 参数（camelCase） | 语义 |
| --- | --- | --- |
| `kafka_ui_search` | `topic` 必填；`connectionId?`、`offsetStrategy?`（latest/earliest/committed/timestamp/offset）、`limit?`、`filter?`、`keyFilter?`、`valueFilter?`、`headerFilter?`、`matchMode?`、`groupId?`、`partitions?[]`、`offsetTime?` | 填 consume 条件并触发消费，结果留 UI；返回 `{intentId, state, summary}`。前端未响应 → `state:"pending"` + 引导走 digest |
| `kafka_ui_focus` | `panel` 必填（messages/topics/groups/schemas）；`connectionId?` | 聚焦面板；同 intent 回报语义 |
| `kafka_ui_select` | `partition`、`offset` 必填；`topic?`、`connectionId?` | 消息结果按 partition+offset 定位并打开详情；未命中 → `rejected` + reason |
| `kafka_ui_state` | `intentId?`、`connectionId?` | 带 intentId 读 intent 结果；不带读最新 UI 快照，且 `connectionId` 给出时附带该连接的 stream 会话状态段（`streams`，设计 §6.3） |
| `kafka_ui_topics` | `connectionId` 必填 | topic 名清单（硬上限 50，纯定位用；帮 AI 在 digest/ui_search 前选定真实 topic 名） |
| `kafka_messages_digest` | `connectionId`、`topic` 必填；`offsetStrategy?`（缺省 earliest）、`offsetTime?`、`partitions?[]`、`maxScanRecords?`（缺省 1000，上限 100000）、`filter?`/`keyFilter?`/`valueFilter?`/`headerFilter?`/`matchMode?`/`fieldFilters?`（consume 同名通道）、`decode?`、`decompression?`、`fields?[]`（JSON path 投影）、`format?`（digest 缺省 / rows） | **本地读核心**：一次性 Consume（maxScanRecords 语义）+ sidecar 本地聚合 `{matched, scanned, scanTruncated, stats:{perPartition、keys（含 nullKeyCount）、timeHistogram（≤12 桶）、fields[]（distinct/topN ≤10）}, sample[≤5], cursorId}`；`format:"rows"` 出 ≤20 行定位字段行。消息体不离开 sidecar；超 512KB 被截断的 value 在样本中以占位符 + 定位指引出现 |
| `kafka_cursor_next` | `cursorId` 必填；`n?`（≤20，缺省 20）、`offset?`（缺省续读） | digest 会话翻页：只取 topic-partition-offset（+ 投影字段）行，条件不重发、远端不重扫。会话 TTL 10 分钟、LRU ≤8、物化上限 1 万行；过期报错建议重发 digest |
| `kafka_messages_produce` | `connectionId`、`topic` 必填；`key?`/`keyBase64?`、`value?`/`valueBase64?`、`headers?`、`partition?`、`compression?`、`schema?` | 单条小消息**单阶段直执行**（value/valueBase64 合计 ≤64 KiB，超出提示走工作台）；审计 `source:"mcp"` |
| `kafka_topics_delete` | `connectionId`、`topics[]` 必填；`confirmToken?` | **强制两阶段**，见下节 |
| `kafka_groups_offsets_reset` | `connectionId`、`group`、`resetTo` 必填；`topics?[]`、`timestampMs?`、`partitionOffsets?`、`confirmToken?` | **强制两阶段**，见下节 |
| `kafka_topics_records_clear` | `connectionId`、`topic` 必填；`confirmToken?` | **强制两阶段**，见下节 |

## 写路径与两阶段确认（§4）

```
第一次  kafka_topics_delete {connectionId, topics:["orders"]}   （无 confirmToken）
  → {preview:{connectionId, topics, confirmTopic…},
     confirmToken:"c-…", expiresAt, note}                       （不执行任何写）
第二次  同参数 + confirmToken 且参数 hash 一致 → 执行 + 审计
```

- **单阶段直执行**：`kafka_messages_produce`（非破坏、可逆；照常过
  read_only 门）。
- **强制两阶段**：`kafka_topics_delete`、`kafka_groups_offsets_reset`、
  `kafka_topics_records_clear`。confirmToken 一次性、60s TTL、与请求参数
  hash 绑定——参数被改即作废（`arguments changed…`），过期（`expired`）
  或复用（`unknown or already used`）都要求重开预览。
- MCP 面没有 `confirmTopic` 参数（两阶段令牌即确认）；第二阶段执行时
  sidecar 内部仍按 topics 填充同名确认字段，kafkaconn 的防误删/防误清空
  门禁保持有效（纵深防御）。
- **只读/删除门**：只读连接上写工具不进 `mcp/tools` 清单，
  `allow_delete=false` 追加剔除删除类两工具；`mcp/call` 侧再拒绝一道
  （`PolicyOf` 同源）。
- **审计**：MCP 写路径审计记 `source:"mcp"`（`kafka/audit` 事件与
  audit.jsonl 同条携带；additive 字段，工作台路径不携带该字段，M0 审计
  事件形状不变）。
- **响应上限**：单工具响应 16 KiB（`mcp/settings/set` 的
  `responseLimitBytes`，1 KiB–1 MiB），超限按 sample→rows→stats 顺序丢弃
  并置 `truncated:true`，仍超限返回占位响应。

## mcp/settings（可调参数，`mcp-settings.json` 持久化）

| 字段 | 默认 | 范围 | 说明 |
| --- | --- | --- | --- |
| `reportWaitMs` | 5000 | 1–30000 | UI intent report 等待时长 |
| `cellWidth` | 120 | 1–2000 | 单元格截断宽度（partition/offset 定位字段不截断） |
| `digestGroupLimit` | 20 | 1–20 | per-partition / key groupBy 组数上限 |
| `digestTopN` | 10 | 1–10 | 字段投影 distinct/topN 取样上限 |
| `digestSampleRows` | 5 | 1–5 | digest 样本行数 |
| `digestRowLimit` | 20 | 1–20 | `format:"rows"` 行数 |
| `digestScanLimit` | 1000 | 1–100000 | digest 扫描上限（kafka 域内扩展，maxScanRecords 语义） |
| `responseLimitBytes` | 16384 | 1024–1048576 | 单响应上限 |

## 降级矩阵（设计 §5）

| 场景 | `kafka_ui_*` | digest / cursor | 写工具 |
| --- | --- | --- | --- |
| 工作台打开、前端在线 | applied + summary | 可用 | 可用 |
| 前端在线但面板无该 action | rejected + reason | 可用 | 可用 |
| 工作台未打开 / 前端未响应 | pending → 超时 hint（引导走 digest） | 可用 | 可用 |
| 连接只读 | 不受影响 | 可用 | 不注册进工具清单 |
| allow_delete=false | 不受影响 | 可用 | 仅删除类两工具不注册 |
| Scoped AI 会话 | 宿主禁 `dbx_call_plugin_tool` | 同左 | 同左 |

## 状态机验收用例

digest/cursor/confirmToken/intent 纯逻辑的验收用例清单（三插件同表，防
形状漂移）单点维护在 `shared/frontend/README.zh-CN.md`「MCP 两阶段/
digest/cursor 验收用例清单」，kafka 侧对应
`backend/internal/mcp/*_test.go`（S-SET/S-INT/S-CUR/S-CONF + S-DIG 的
kafka 变体：per-partition 计数、key groupBy、时间直方图、字段投影聚合；
S-SRV 编排）。

## smoke

```bash
DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-kafka python3 scripts/smoke_mcp.py
# 场景 K1–K11：离线 10 个（settings/tools/只读与 allow_delete 门/intent
# pending 与 applied/快照+streams/门禁/未知方法）+ K11 容器场景（digest
# 聚合 + cursor 翻页 + produce + 三个两阶段写全流程 + token 复用拒绝 +
# audit source=mcp）。未注册方法 SKIP 不 FAIL（M0 §5.2）；dev 测试集群用
# `bash scripts/dev-cluster.sh up` 拉起。
```
