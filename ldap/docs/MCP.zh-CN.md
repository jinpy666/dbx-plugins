# MCP 集成（M1）

ldap 插件的 MCP 工具面（设计来源 `shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md`
v2，ssh `mcp.rs` 骨架的 Go 移植）。实现在 sidecar `internal/mcp/`（Go），
经 DBX MCP 桥（`dbx_list_plugin_tools` / `dbx_call_plugin_tool`）调用插件
协议方法 `mcp/tools`、`mcp/call`、`mcp/settings/get|set`。桥按
`connectionId` 转发标准 lifecycle payload（`mcp/call` 的 `lifecycle` 字段），
凭据由宿主解析，**工具参数里不出现任何密码**。

核心设计（详见设计文档）：

1. **读：UI 优先 + 强本地化**——大结果集不进 MCP；AI 用 UI intent 把条件
   填进工作台（用户可视可继续操作），或用 digest 在 sidecar 本地聚合 +
   cursor 翻页，扫描过程数据一条不出 sidecar。
2. **写：入 MCP 面 + 两阶段确认**——add/modify 单阶段；delete（含
   recursive）/modifyDn 强制 preview → confirmToken。
3. **token 经济**——默认 `format:"digest"`（计数 + 分布 + 样本），显式要
   `rows` 才出行且 clamp ≤20 行；单响应上限 16 KiB。

## 事件：`ldap/ui/intent`（sidecar → 前端）

sidecar 收到 UI 驱动类工具调用时：生成 `intentId` → intent 状态表登记
（进程内 map，TTL 60s，LRU 20 条，`pending`）→ 发本事件 → 等待前端
report（默认 5s，`mcp/settings/set` 的 `reportWaitMs` 可调）。

```json
{ "intentId": "i-1a2b3c4d", "action": "search",
  "params": { "baseDn": "…", "filter": "(uid=a)", "scope": "sub",
              "attributes": ["cn"], "sizeLimit": 500, "connectionId": "…" } }
```

| action | params | 前端行为 |
| --- | --- | --- |
| `search` | `baseDn?`、`filter`、`scope?`、`attributes?[]`、`sizeLimit?` | SearchForm 填条件（过滤器走既有 preset 反序列化路径）→ 触发搜索 → 结果进 ResultTable |
| `focus` | `panel`（search \| tree \| schema） | 切换/聚焦面板，关闭遮挡弹窗 |
| `select` | `dn` | 当前结果表内按 DN 定位命中行并打开条目编辑器 |

前端消费统一走 `shared/frontend/uiIntent.ts` 的 `useUiIntent("ldap",
handlers)`（公共层单点维护，插件不各抄一份）。

## 方法：`ldap/ui/state/report`（前端 → sidecar）

- **intent 回报**：`{intentId, status: "applied"|"rejected", summary,
  reason?}`。summary = `{count, truncated?, rows[≤5], anchor?, reason?}`，
  每 cell 截 120 字符（`cellWidth` 可调），**DN 定位字段不截断**。
- **快照型**：无 `intentId`、`status:"snapshot"`——前端在关键动作后（搜索
  完成、条目打开、面板切换）主动上报 `{panel, baseDn?, filter?, count?,
  anchor?}`，sidecar 缓存最新快照；`ldap_ui_state` 不带 intentId 时返回。
- 未知/已过期 intentId 回报报业务错误（-32000）。

## 工具一览（8 个）

`mcp/tools` 可带可选 `{connectionId}`：该连接配置为只读（表单
`read_only` ∥ 宿主标准 read_only）时，写工具**不进清单**并附
`omittedWriteTools` 原因说明；未带 connectionId 时全量列出（调用时仍有
只读门拒绝，纵深防御）。Scoped AI 会话由宿主禁 `dbx_call_plugin_tool`。

| 工具 | 参数（camelCase） | 语义 |
| --- | --- | --- |
| `ldap_ui_search` | `filter` 必填；`connectionId?`、`baseDn?`、`scope?`（base/one/sub）、`attributes?[]`、`sizeLimit?` | 填条件并触发搜索，结果留 UI；返回 `{intentId, state, summary}`。前端未响应 → `state:"pending"` + 引导走 digest |
| `ldap_ui_focus` | `panel` 必填（search/tree/schema）；`connectionId?` | 聚焦面板；同 intent 回报语义 |
| `ldap_ui_select` | `dn` 必填；`connectionId?` | 结果表定位 DN 并打开条目；命中失败 → `rejected` + reason |
| `ldap_ui_state` | `intentId?` | 带 intentId 读 intent 结果（applied/rejected/pending/expired）；不带读最新 UI 快照 |
| `ldap_ui_schema` | `connectionId` 必填 | schema 缓存的 attributeType/objectClass 名称清单（帮 AI 构造合法 filter；名称截 300 个防响应膨胀） |
| `ldap_search_digest` | `connectionId`、`filter` 必填（缺省 `(objectClass=*)`）；`baseDn?`、`scope?`（缺省 sub）、`attributes?[]`（投影）、`distinctAttr?`、`format?`（digest 缺省 / rows） | **本地读核心**：filter 服务端执行，sidecar 本地聚合 `{matched, scanned, truncated, stats:{objectClass 分布、subtrees 子树计数、distinct 值域}, sample[≤5], cursorId}`；`format:"rows"` 出 ≤20 行。扫描上限 1000 条（`sizeLimit` 可调低）。二进制/大属性不特判出 MCP（LDAP 值随服务端投影返回，属性过 120 字符截断） |
| `ldap_cursor_next` | `cursorId` 必填；`n?`（≤20，缺省 20）、`offset?`（缺省续读） | digest 会话翻页：只取 DN + 投影属性行，条件不重发、远端不重扫。会话 TTL 10 分钟、LRU ≤8、物化上限 1 万行；过期报错建议重发 digest |
| `ldap_entry_write` | `connectionId`、`action`（add/modify/delete/modifyDn）、`dn` 必填；按 action 附 `attributes` / `changes` / `recursive` / `newRdn` / `deleteOldRdn` / `newSuperior` / `confirmToken` | 写入口，见下节两阶段语义 |

## 写路径与两阶段确认（§4）

```
第一次  ldap_entry_write {action:"delete", dn:"…"}          （无 confirmToken）
  → {preview:{action, dn, recursive?, childCount?|destinationDn},
     confirmToken:"c-…", expiresAt, note}                    （不执行任何写）
第二次  同参数 + confirmToken 且参数 hash 一致 → 执行 + 审计
```

- **单阶段直执行**：`add` / `modify`（非破坏、可逆；照常过写白名单与
  屏蔽属性策略）。
- **强制两阶段**：`delete`（含 recursive，沿用子树 1000 条上限语义）与
  `modifyDn`。confirmToken 一次性、60s TTL、与请求参数 hash 绑定——参数
  被改即作废（`arguments changed…`），过期（`expired`）或复用（`unknown
  or already used`）都要求重开预览。
- **只读门**：只读连接上写工具不进 `mcp/tools` 清单，`mcp/call` 侧再拒绝
  一道；写策略（DN 白名单、屏蔽属性）与工作台 `ensure_writable` 同源。
- **审计**：所有 MCP 写路径审计记 `source:"mcp"`（`ldap/audit` 事件与
  audit.jsonl 同条携带；M0 审计事件形状不变，新增可选字段）。
- **响应上限**：单工具响应 16 KiB（`mcp/settings/set` 的
  `responseLimitBytes`，1 KiB–1 MiB），超限按 sample→rows→stats 顺序丢弃
  并置 `truncated:true`，仍超限返回占位响应。

## mcp/settings（可调参数，`mcp-settings.json` 持久化）

| 字段 | 默认 | 范围 | 说明 |
| --- | --- | --- | --- |
| `reportWaitMs` | 5000 | 1–30000 | UI intent report 等待时长 |
| `cellWidth` | 120 | 1–2000 | 单元格截断宽度（DN 不截断） |
| `digestGroupLimit` | 20 | 1–20 | groupBy 组数上限 |
| `digestTopN` | 10 | 1–10 | distinct/topN 取样上限 |
| `digestSampleRows` | 5 | 1–5 | digest 样本行数 |
| `digestRowLimit` | 20 | 1–20 | `format:"rows"` 行数 |
| `responseLimitBytes` | 16384 | 1024–1048576 | 单响应上限 |

## 降级矩阵（设计 §5）

| 场景 | `ldap_ui_*` | digest / cursor | 写工具 |
| --- | --- | --- | --- |
| 工作台打开、前端在线 | applied + summary | 可用 | 可用 |
| 前端在线但面板无该 action | rejected + reason | 可用 | 可用 |
| 工作台未打开 / 前端未响应 | pending → 超时 hint（引导走 digest） | 可用 | 可用 |
| 连接只读 | 不受影响 | 可用 | 不注册进工具清单 |
| Scoped AI 会话 | 宿主禁 `dbx_call_plugin_tool` | 同左 | 同左 |

## 状态机验收用例

digest/cursor/confirmToken/intent 纯逻辑的验收用例清单（三插件同表，防
形状漂移）单点维护在 `shared/frontend/README.zh-CN.md`「MCP 两阶段/
digest/cursor 验收用例清单」，ldap 侧对应 `backend/internal/mcp/*_test.go`
（S-SET/S-INT/S-CUR/S-CONF/S-DIG/S-SRV）。

## smoke

```bash
DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-ldap python3 scripts/smoke_mcp.py
# 场景 M1–M10：离线 9 个（settings/tools/只读门/intent pending 与
# applied/快照/门禁/未知方法）+ M10 容器场景（digest+cursor+两阶段删除
# 往返）。未注册方法 SKIP 不 FAIL（M0 §5.2）。
```
