# MCP 集成（M1）

ldap 插件的 MCP 工具面（设计来源 `shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md`
v2，ssh `mcp.rs` 骨架的 Go 移植）。实现在 sidecar `internal/mcp/`（Go），
有**两种被 AI 客户端调用的方式**：

- **方式一（推荐）：DBX MCP 桥**——经 `dbx_list_plugin_tools` /
  `dbx_call_plugin_tool` 调用插件协议方法 `mcp/tools`、`mcp/call`、
  `mcp/settings/get|set`。桥按 `connectionId` 转发标准 lifecycle payload
  （`mcp/call` 的 `lifecycle` 字段），凭据由宿主解析，**工具参数里不出现
  任何密码**。
- **方式二：独立 stdio 模式（`--mcp`）**——sidecar 二进制自起 MCP 服务器，
  无需 DBX 在场，凭据随调用内联传入（见下文专节）。

核心设计（详见设计文档）：

1. **读：UI 优先 + 强本地化**——大结果集不进 MCP；AI 用 UI intent 把条件
   填进工作台（用户可视可继续操作），或用 digest 在 sidecar 本地聚合 +
   cursor 翻页，扫描过程数据一条不出 sidecar。
2. **写：入 MCP 面 + 两阶段确认**——add/modify 单阶段；delete（含
   recursive）/modifyDn 强制 preview → confirmToken。
3. **token 经济**——默认 `format:"digest"`（计数 + 分布 + 样本），显式要
   `rows` 才出行且 clamp ≤20 行；单响应上限 16 KiB。

## 方式二：独立 stdio 模式（`--mcp`）

sidecar 二进制直接作为 MCP 服务器运行（MCP `2024-11-05`，换行分隔
JSON-RPC 2.0 over stdio；ssh `run_mcp_stdio` 的 Go 移植）：

```bash
dbx-plugin-ldap --mcp
```

- **入口互斥**：`--mcp` 与 DBX 插件协议模式互斥，同一进程只跑其中一种；
  stdio 模式不启动 Emitter/插件协议循环（intent 事件通道不存在）。
- **协议方法**：`initialize`（serverInfo.name=`io.dbx.ldap`，version 取
  构建版本）、`notifications/initialized`（通知不回包）、`tools/list`、
  `tools/call`、`ping`；未知方法返回 JSON-RPC 错误（-32601）不崩。请求逐
  个 goroutine 处理，慢 digest 不阻塞 `ping`/`tools/list`（响应可能乱序，
  以 id 关联）；stdin EOF 后 drain 在途请求至多 300s 再退出。
- **请求形状分档（2026-09-13 可靠性纵深轮）**：解析失败 -32700（id null）；
  非法请求 -32600——缺 id（非通知）、`id` 为 object/array/布尔、`method`
  缺失/空/非字符串、`jsonrpc` 存在且非 `"2.0"`（字段缺失容忍，照 ssh 基线）；
  `tools/call` params 非对象 -32602。全部结构化报错，进程不崩、后续合法
  请求照常服务；非法 UTF-8 字节流按顶层结构损坏归 -32700。
- **单行上限 16 MiB**：超限行直接 -32700 拒绝该行并继续服务（防单行无界
  占内存；8 MiB 级 arguments 走正常分派、工具层优雅报错）。smoke M18 钉住
  全矩阵（非法 JSON/UTF-8、通知静默、形状分档、8 MiB 行、pipelining 3+、
  空行/CRLF）。
- **tools/list**：复用 `mcp/tools` 既有注册表（工具名/描述/语义不变），
  并为连接类工具（`ldap_search_digest` / `ldap_entry_write`）在
  inputSchema 里**显式声明内联连接参数**（严格校验的 MCP 宿主会丢弃未
  声明参数，ssh 同因声明），`required` 的 `connectionId` 放宽为 anyOf
  二选一（`connectionId` 或内联 `host`）。UI 类工具 schema 保持原样。

### 内联凭据连接（与连接表单字段对齐，camelCase）

连接参数随每次工具调用内联传入（**不落盘、不持久化**；凭据只进进程内存
连接表与参数 hash 输入），按参数 hash 池化为进程内 connectionId
（`mcp-<hash 前 32 hex>`）：同参数后续调用自动复用（底层连接与工作台
同一套 service，惰性建连 + 断线重连语义保持）；池上限 8（`maxCursorSessions`
无关，写死常量），满后淘汰最旧并断开。也可以传上次返回调用里用过的池化
`connectionId` 复用；传 DBX 保存连接的 id 则走**桥接兜底**（见下节）。

| 参数 | 表单字段 | 说明 |
| --- | --- | --- |
| `host` 必填 | `host` | 裸主机名（或完整 ldap/ldaps URL 透传） |
| `port` | `port` | 缺省按 scheme（389 / 636） |
| `tlsMode` | `tls_mode` | `none`（缺省）\| `starttls` \| `ldaps` |
| `startTls` | —（便捷别名） | `true` 等价 `tlsMode:"starttls"`（tlsMode 已给时忽略） |
| `authType` | `auth_type` | `simple`（缺省）\| `anonymous` \| `unauthenticated` |
| `bindDn` | `bind_dn` | simple bind DN |
| `username` / `domain` | `username` / `domain` | NTLM 等账户字段 |
| `password` | `bind_password`（secret） | 别名 `bindPassword` |
| `ntlmHash` | `ntlm_hash`（secret） | NT hash bind |
| `baseDn` | `base_dn` | 缺省 Base DN |
| `tlsVerify` | `tls_verify` | 缺省 true |
| `tlsCaPath` / `tlsServerName` | `tls_ca_path` / `tls_server_name` | TLS 校验 |
| `timeoutSecs` | `timeout_secs` | 缺省 30 |
| `readOnly` | `read_only` | 只读门（写工具拒绝） |

**stdio 未覆盖的表单字段**（本轮内联不支持，需工作台保存连接）：Kerberos
族（`krb_*`）、DIGEST-MD5/SASL 细化（`sasl_host`/`sasl_qop`/`sasl_mutual_auth`）、
DN 白名单与屏蔽属性策略（`allowed_base_dns` / `allowed_write_base_dns` /
`blocked_attributes`，内联连接回落内置默认屏蔽表）。

### stdio 语义差异

- **UI 类工具（`ldap_ui_*` 全部 5 个）照常列出，但 `tools/call` 返回明确
  `UNAVAILABLE`**：`isError:true` + 内容「UNAVAILABLE: 此工具需要 DBX
  工作台（工作台模式可用，请经 DBX MCP 桥调用）……」，不假死、不长时间
  挂起（设计 §5 stdio 行）。
- digest/cursor/写族语义与方式一完全同构（同一 `mcp/call` 分派路径，只是
  新入口）：filter 服务端执行 + 本地聚合、cursor 翻页、两阶段 confirmToken
  （一次性/60s/hash 绑定）、写审计 `source:"mcp"` 照常落 audit.jsonl。
- 工具执行错误按 MCP 规约以 `isError:true` content 返回（非协议级错误）。
- 数据目录沿用 `DBX_PLUGIN_DATA_DIR`（缺省平台持久用户数据目录），
  `mcp-settings.json`（digest/截断参数）与 `audit.jsonl` 都在其中生效。

### 桥接兜底（同 ssh 模式，2026-09-13 第二轮落地）

stdio 模式带「未注册 `connectionId` 自动转发运行中 DBX 应用本地 TCP 桥」
的兜底（ssh `app_bridge.rs` 的 Go 移植，`internal/mcp/appbridge.go`）：

- **转发决策**：连接类工具（`ldap_search_digest` / `ldap_entry_write`）收到
  本会话未池化的 `connectionId` 时，先经桥转发给应用自己的 sidecar（保存
  连接的凭据/只读标志/策略由应用侧持有，工具参数里无凭据）；已池化 id 走
  本地路径不变，内联凭据优先级最高（在内即不走桥）。
- **发现与契约**：应用把端口写在 `<app_data_dir>/mcp-bridge-port`
  （`DBX_APP_DATA_DIR` 覆盖，缺省 macOS `~/Library/Application Support/
  com.dbx.app`），`POST /call-plugin-tool` body 为 snake_case 五字段
  `{plugin_id:"io.dbx.ldap", connection_id, tool, arguments, timeout_ms}`；
  200 body（应用侧 MCP content envelope）逐字透传。转发超时跟随
  `timeoutSecs`（clamp 5–300，缺省 300）+ 150s 审批读余量。
- **fail-closed**：端口文件缺失/损坏/TCP 探测失败/HTTP 非 200 都立即返回
  可行动错误（带 `DBX app bridge` 失败原因 + 三条出路：内联凭据 / 启动
  DBX 应用 / 工作台），不假死、不静默重拨。应用未起时先尽力拉起
  （`DBX_APP_LAUNCH_CMD` 覆盖，缺省 `open -a DBX.app`）并轮询端口至多
  30s（`ensure` 预算，TCP 探测防陈旧端口）。
- **两阶段写透传**：preview/confirm 全部在应用侧 sidecar 完成一次性与
  hash 绑定语义（本地 stdio 会话不参与，token 不跨侧通用）。
- 测试：Go 单测 mock 桥（httptest，契约形状/envelope 透传/404 表面化/
  fail-closed）；smoke M14（空 app-data fail-closed + 本地 mock 桥转发）。

### 接入 ZCode（stdio 客户端）

```json
{
  "mcp": {
    "servers": {
      "dbx-ldap": {
        "type": "stdio",
        "command": "/绝对路径/backend/bin/dbx-plugin-ldap",
        "args": ["--mcp"]
      }
    }
  }
}
```

真机回环验证：`python3 scripts/smoke_mcp.py`（M11 离线 stdio / M12 容器
内联凭据全流程）；手工冒烟
`echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | dbx-plugin-ldap --mcp`。

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
- 未知/已过期 intentId 回报报业务错误（-32000），错误附自纠指引（2026-09-13
  第三轮补齐，对齐 files 同款）：intent 为进程内状态、60s 过期，重发
  `ldap_ui_*` 调用，或省略 intentId 读最新快照。

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
| `ldap_search_digest` | `connectionId`、`filter` 必填（schema 声明必填；实现层空串兜底 `(objectClass=*)`）；`baseDn?`、`scope?`（缺省 sub）、`attributes?[]`（投影）、`distinctAttr?`、`format?`（digest 缺省 / rows） | **本地读核心**：filter 服务端执行，sidecar 本地聚合 `{matched, scanned, truncated, stats:{objectClass 分布、subtrees 子树计数、distinct 值域}, sample[≤5], cursorId}`；`format:"rows"` 出 ≤20 行。`distinctAttr` 自动并入远端投影（不必与 `attributes` 同传）；零命中时 `stats.objectClass/subtrees` 恒为 `{}`（键不消失）。扫描上限 1000 条（`sizeLimit` 可调低 / `digestScanLimit` settings 可调高）。二进制/大属性不特判出 MCP（LDAP 值随服务端投影返回，属性过 120 字符截断） |
| `ldap_cursor_next` | `cursorId` 必填；`n?`（≤20，缺省 20）、`offset?`（缺省续读） | digest 会话翻页：只取 DN + 投影属性行，条件不重发、远端不重扫。空批恒回 `rows:[]`（非 null）。会话 TTL 10 分钟、LRU ≤8、物化上限 1 万行（均可经 settings 调整）；过期/未知（可能被 LRU 淘汰）报错携带实际生效 TTL/容量并建议重发 digest |
| `ldap_entry_write` | `connectionId`、`action`（add/modify/delete/modifyDn）、`dn` 必填；按 action 附 `attributes` / `changes` / `recursive` / `newRdn` / `deleteOldRdn` / `newSuperior` / `confirmToken` | 写入口，见下节两阶段语义 |

## 参数容错（LLM 输入变体，2026-09-13 专项固化）

AI agent 的常见输入变体按「能明确解析则宽容、语义有歧义则清晰报错」处理：

- **整数参数**（`sizeLimit` / `n` / `offset` / stdio `port` / `timeoutSecs` /
  `mcp/settings/set` 数值字段）：JSON number 与字符串数字（`"50"`、`"20.0"`）
  等价；非数字串报 `must be a positive integer`（settings）或按缺省兜底。
- **`distinctAttr`**：自动并入 digest 远端投影——只传 `distinctAttr` 不传
  `attributes` 时 distinct 聚合照常工作（不静默返回空值域）。
- **scope**：大小写不敏感并接受常见别名（`baseObject`→base、
  `singleLevel`→one、`subtree`/`wholeSubtree`→sub）；空串 = 连接缺省 sub；
  **未知值直接报错**（`scope must be base, one, or sub (aliases
  baseObject/singleLevel/subtree accepted; got …)`——schema enum 值与 RFC
  别名都列出，在线钉桩 M20），不静默按 sub 扫描——scope 决定结果集范围，
  含糊兜底会让 AI 误判匹配数量。
- **缺参枚举（2026-09-14 第七轮，对齐 ssh §3.9）**：`required` 参数缺失
  （键不存在或显式 `null`）时**一次枚举全部缺口**：
  `Missing required parameters: connectionId, action, dn`（按 schema
  `required` 声明顺序），LLM 一轮补齐所有缺口而不是逐个 fail-fast 往返；
  present-but-类型错误（空串/类型不符）不混入枚举，仍由逐参数校验精确点名
  （如空 `filter` → `filter is required (RFC 4515…)`）。`search_digest` 的
  `filter` 随此对齐 schema `required`——缺失不再静默回退
  `(objectClass=*)` 全扫（忘传条件变成整树扫描，准确性 + 成本双输）。
- **`attributes`**：字符串数组之外也接受逗号分隔串（`"cn,mail"`）；数组元素
  数字/布尔转字符串；空白项丢弃、按书写形态去重。
- **写 `attributes` / `changes[].values`**：单字符串值折算单元素数组
  （`{"ou": "people"}`），数字/布尔值转字符串（`employeeNumber:[42]`→`"42"`）；
  空值列表/非标量元素明确报错不吞值。
- **`ldap_ui_focus` 的 `panel`**：大小写不敏感归一化；非法值本地报错并列出
  合法值（不发注定 rejected 的 intent，省一轮 TTL 等待）。
- **错误自纠**：`unknown tool` 附全部可用工具名；`unknown cursorId` 附
  TTL/淘汰说明与「重发 `ldap_search_digest`」指引；UI intent 超时 hint 引导
  走 digest。

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
  `modifyDn`。confirmToken 一次性、hash 绑定、与请求参数（含 `connectionId`）
  一致才执行——参数被改即作废（`arguments changed…`），**换连接复用同样
  作废**（防连接 A 开预览、连接 B 执行的跨连接误删），过期
  （`expired`，报文携带实际生效 TTL）或复用（`unknown or already used`）
  都要求重开预览。TTL 缺省 60s，`mcp/settings/set` 的 `confirmTtlSecs`
  可调（10–600，files 同名同范围）。
- **只读门**：只读连接上写工具不进 `mcp/tools` 清单，`mcp/call` 侧再拒绝
  一道；写策略（DN 白名单、屏蔽属性）与工作台 `ensure_writable` 同源。
- **预检前置（2026-09-13 可靠性纵深轮）**：两阶段 preview 签发一次性令牌
  之前先做三类本地校验，注定失败的参数不再"预览成功 → 确认才报错"地白烧
  令牌：(1) DN 结构校验（空 DN + 非法 DN）；(2) 写白名单
  （`allowed_write_base_dns`，空则回退 `allowed_base_dns`）；(3) 只读连接
  （既有第二道门不变）。**DN 控制字符加固**：go-ldap `ParseDN` 对裸换行/
  空字节等控制字符不报错（实测放行、会直达服务端且原样落 audit.jsonl 构成
  审计日志注入面），写 DN 含 `<0x20`/`0x7f` 裸控制字符一律本地报
  `invalid dn`（RFC 4514 转义形态 `\0A` 等不受影响）；该加固在
  `ldapconn.normalizeLDAPWriteDN` 单点生效，工作台写路径同享。
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
| `digestScanLimit` | 1000 | 1–100000 | digest 远端扫描上限（kafka 同名同范围） |
| `maxCursorRows` | 10000 | 1–100000 | cursor 物化行数上限（files 同名） |
| `cursorTtlSecs` | 600 | 1–3600 | cursor 会话 TTL（files 同名） |
| `maxCursorSessions` | 8 | 1–32 | cursor LRU 容量（files 同名） |
| `confirmTtlSecs` | 60 | 10–600 | confirmToken TTL（files 同名同范围） |
| `responseLimitBytes` | 16384 | 1024–1048576 | 单响应上限 |

cursor/confirm TTL 与容量变更即时生效于下一次会话（已存在会话不追溯）；
cursor 过期/未知错误与 confirmToken 过期错误均携带实际生效值。cursor 淘汰
为**真 LRU**（命中续读把会话顶到淘汰序队尾，持续翻页的活跃会话不被纯插入
序误逐；2026-09-13 可靠性纵深轮）；confirmToken 表在每次签发时顺手清理
已过期未消费令牌（大量「只要预览不确认」的调用不再无界撑表）。

## 降级矩阵（设计 §5）

| 场景 | `ldap_ui_*` | digest / cursor | 写工具 |
| --- | --- | --- | --- |
| 工作台打开、前端在线 | applied + summary | 可用 | 可用 |
| 前端在线但面板无该 action | rejected + reason | 可用 | 可用 |
| 工作台未打开 / 前端未响应 | pending → 超时 hint（引导走 digest） | 可用 | 可用 |
| 独立 stdio `--mcp` | 明确 `UNAVAILABLE`（"此工具需要 DBX 工作台"），不假死 | 可用（凭据内联/池化；未池化 `connectionId` 自动桥接兜底） | 可用（同左） |
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
# 场景 M1–M20：离线 11 个（settings/tools/只读门/intent pending 与
# applied/快照/门禁/未知方法/LLM 输入变体容错 + M11 stdio `--mcp`
# initialize/tools/UNAVAILABLE/连接门）+ 容器场景（M10 digest+cursor+两阶段
# 删除往返+token 换连接作废；M12 stdio 内联凭据真实查询 + 两阶段写全流程
# + audit source=mcp；M15 digest 聚合变体：groupBy/distinct/topN/子树计数/
# clamp/零命中/空聚合键；M16 ldap_ui_schema 冷热缓存 + 300 名截断 + 断连
# 报错；M17 cursor 深翻页/显式 offset 重读/越界 clamp/settings TTL 接线）
# + M14 stdio 桥接兜底（空 app-data fail-closed + 本地 mock 桥转发契约 +
# 404 表面化；约 30s ensure 预算）。可靠性纵深轮（2026-09-13）新增：M18
# stdio 健壮性（非法 JSON/UTF-8、通知静默、请求形状分档 -32600、8 MiB 行、
# pipelining、空行/CRLF，全程进程存活，离线）+ M19 重复 digest 幂等/cursor
# 重读稳定/独立 preview token（容器）。第七轮（2026-09-14）新增：M8 缺参
# 枚举断言（entry_write 三缺全点名/单缺只其一/null 视同缺失）+ M20 enum
# 非法值在线钉桩（真实连接下 scope/action 传 bogus，报错列出 schema enum
# 全部合法值；scope="SUBTREE" 大小写归一正常消费）。未注册方法 SKIP 不
# FAIL（M0 §5.2）。
```
