# dbx-ldap-plugin 可实施文档

> 上游：`PLUGIN_PROPOSAL_LDAP_RCLONE.zh-CN.md`（定稿）；公共基线：
> `IMPL_PLAN_M0_COMMON.zh-CN.md`（SDK/打包/lifecycle/审计/测试基建，下称 M0 文档）。
> 插件：`io.dbx.ldap`，仓库 `~/btroot/dbx-plugins/ldap`，sidecar `dbx-plugin-ldap`（Go）。
> 能力来源：tiny-rdm `backend/services/ldap_service.go` 等（file:line 见迁移映射表）。

## 0. 目标与非目标

**目标**：对齐 tiny-rdm LDAP Console 全部能力——19 个服务方法语义、8 种认证、
DN 树/搜索/条目编辑/LDIF/CSV/预设前端、DN 白名单与屏蔽属性安全策略、
schema 元数据、（M4）14 个 MCP 工具。

**非目标**（方案 D3/D5，DBX 语义替代）：
- SQLite profile 存储 → 宿主 connection-provider；
- SSH 隧道/代理/endpoint rewrite → DBX 传输层（拨 `runtime.host:port`）；
- `ldapi://` Unix socket（Phase 2 视宿主需求）；password modify / WhoAmI /
  Compare 扩展操作（tiny-rdm 亦无，不对齐不缺失）。

**分期**：M1 simple/anonymous + 核心浏览编辑；M3 其余 6 种认证；M4 MCP 工具。

## 1. 仓库结构

```
dbx-ldap-plugin/
├── manifest.json / dbx-plugin.toml / assets/plugin.svg
├── backend/
│   ├── go.mod                          # module io.dbx.ldap.plugin
│   ├── cmd/plugin/main.go              # Metadata + Handler 装配 + 方法分发 switch
│   └── internal/
│       ├── lifecycle/                  # M0-T5 公共包（lifecycle params 解析）
│       ├── store/                      # M0-T5 公共包（prefs/audit/schema-cache）
│       ├── ldapconn/                   # 领域层（自 tiny-rdm 移植，见 §3）
│       │   ├── service.go              # 连接池/withConn 重连/操作入口
│       │   ├── dial.go                 # dial/bind/TLS（8 种认证分发）
│       │   ├── auth_gssapi.go          # kerberos（M3）
│       │   ├── policy.go               # DN 白名单/屏蔽属性/只读/写值转义
│       │   ├── schema.go               # rootDSE/subschema 解析
│       │   └── *_test.go
│       └── ldapgssapi/                 # 自研 GSSAPI 客户端（M3，原样搬运）
├── frontend/                           # Vue3 沙箱，工程配置照 ssh-sftp 插件
│   └── src/{App.vue, components/, lib/}
├── scripts/{build,test}.sh, smoke_test.py, smoke_gssapi_test.py, sidecar_client_jsonl.py
└── docs/{PROTOCOL.zh-CN.md, FEATURE_PARITY.zh-CN.md}
```

## 2. Go 依赖

| 依赖 | 版本 | 用途 |
|---|---|---|
| `github.com/go-ldap/ldap/v3` | v3.4.13（对齐 tiny-rdm） | 协议、SearchWithPaging、GSSAPI/NTLM/Digest 绑定 |
| `github.com/jcmturner/gokrb5/v8` | v8.4.4（对齐） | Kerberos 客户端（M3） |
| `github.com/google/uuid` | latest | operationId/连接 id 兜底 |
| dbx-plugin-sdk（Go） | CLI 捆绑版本 | M0 文档 §1.3 |

无 cgo（gokrb5 纯 Go）；`CGO_ENABLED=0` 交叉编译。

## 3. 代码迁移映射（tiny-rdm → 本插件）

| tiny-rdm 源 | 去处 | 改造点 |
|---|---|---|
| `services/ldap_service.go`（2094 行） | `internal/ldapconn/service.go` + `dial.go` | ① 返回 `types.JSResp` → `(any, *PluginError)`；② profile 从 SQLite/参数 → lifecycle params 构造（§5.1）；③ 去 `ConnectionPoolService` 多窗口引用跟踪，改自管 `map[connectionID]*connEntry`（互斥锁）；④ `withConn` 缓存 + `ldapNeedsReconnect` 断线重连**原样保留**；⑤ 去 `prepareLDAPTransport`/`resolveLDAPTarget`/endpoint rewrite → 拨 `runtime.host:port`，TLS SNI 用 `connection.host`；⑥ 审计 `Audit()` → `store.Audit`（JSONL） |
| `services/ldap_service.go:78-90,1862-1925` | `policy.go` | 默认屏蔽属性表、`ensureLDAPReadAllowed/WriteAllowed`、`dnWithinBase`、`normalizeLDAPWriteValues` 原样；策略参数改从连接配置读 |
| `services/ldap_service.go:773-968` | `schema.go` | subschemaSubentry 发现与 attributeTypes/objectClasses 解析原样 |
| `types/ldap.go` | `ldapconn/types.go` | 保留请求/条目类型；**删** `LDAPTargetConfig`/`CommonTransportConfig`/`LDAPEndpointRewrite`/`MCPPolicyConfig`；`LDAPConnectionProfile` 字段收敛为 §4 manifest 字段 + 运行时派生项 |
| `storage/ldap_profiles.go` | 删除 | 宿主连接管理替代（D3）；预设存 `store.Prefs` |
| `ldapgssapi/client.go` | `internal/ldapgssapi/` | 原样（实现 `ldap.GSSAPIClient`；integrity/confidentiality/mutualAuth 选项） |
| `mcp/tools_ldap.go` + `ldap_redaction.go` | M4 `internal/mcp/` | 工具定义改为 DBX `mcp/tools`+`mcp/call` 形态（照 ssh-sftp `mcp.rs` 模式），白名单/脱敏逻辑原样 |
| kerberos 临时 krb5.conf 写入（ldap_service.go:1626 起） | `auth_gssapi.go` | 原样；落 `DBX_PLUGIN_DATA_DIR/krb5/` |
| `utils/{ldapFilter,dn,ldif,ldapExporter,baseDn}.js` | `frontend/src/lib/` | 原样搬运（RFC 4515 转义、LDIF 序列化/解析、CSV/JSON 导出） |
| `LdapConsolePage.vue`（8543 行） | `frontend/src/` 拆分 | §7 组件树重实现 |

## 4. manifest 贡献点

connection-provider `io.dbx.ldap.connection`，`database_type: "ldap"`，
`capabilities: ["test", "connect", "disconnect"]`，workbench
`io.dbx.ldap.workbench`。字段（key → binding）：

| key | 类型 | binding | 默认 | visible_when (auth_type ∈) |
|---|---|---|---|---|
| `display_name` | text | name | `LDAP server` | — |
| `host` | text | host | `127.0.0.1` | —（v0.1.19 起裸主机名；sidecar 组装 URL，旧完整 URL 连接兼容透传） |
| `port` | number | port | `389` | —（ldaps 时填 636；拨号空缺按 scheme 缺省） |
| `tls_mode` | select | config | `none` | —（none/starttls/ldaps；取代旧 `use_starttls`） |
| `base_dn` | text | config | 空 | — |
| `auth_type` | select | config | `simple` | —（8 选项：anonymous/unauthenticated/simple/kerberos/ntlm/ntlm_hash/digest_md5/external） |
| `bind_dn` | text | config | 空 | simple |
| `username` | text | config | 空 | simple/ntlm/ntlm_hash/digest_md5/kerberos |
| `domain` | text | config | 空 | ntlm/ntlm_hash/digest_md5 |
| `bind_password` | password | secret | 空 | simple/digest_md5（required_when simple） |
| `ntlm_hash` | password | secret | 空 | ntlm_hash |
| `tls_verify` | boolean | config | true | — |
| `tls_ca_path` | text | config | 空 | — |
| `tls_server_name` | text | config | 空 | —（缺省用 connection.host） |
| `krb_credential_type` | select | config | `password` | kerberos |
| `krb_realm` / `krb_kdc_host` / `krb_kdc_port` | text/number | config | 空/88 | kerberos |
| `krb_keytab_path` / `krb_ccache_path` / `krb5_conf_path` | text | config | 空 | kerberos（按 credential_type 二级联动） |
| `krb_password` | password | secret | 空 | kerberos ∧ credential_type=password |
| `timeout_secs` | number | config | 30 | — |
| `read_only` | boolean | config | false | — |
| `allowed_base_dns` / `allowed_write_base_dns` | textarea | config | 空（=不限） | — |
| `blocked_attributes` | textarea | config | 内置 11 项默认表 | — |

> M0-T1/T6 曾按 `url` 绑定 `host`（完整 `ldap(s)://host:port`）落地；真机
> 验证发现宿主把 `connection.host` 原文透传进 `runtime.host`，完整 URL 会
> 污染拨号目标（PROGRESS-A §13）。v0.1.19 起切换到备份方案：`host` 存裸
> 主机名 + `port`/`tls_mode` 结构化字段，sidecar `buildLDAPURL` 组装 URL；
> 旧连接的完整 URL 原样透传（方法契约不变）。

## 5. Sidecar 方法契约

公共约定：参数/返回 camelCase；除生命周期方法外均必填 `connectionId`
（string）；错误 = `PluginError{-32000, message}`；写操作受 `read_only` 与
DN 白名单约束（§6）。方法未注册返回 -32601。

### 5.1 生命周期

- `connection/test`：lifecycle params（M0 文档 §3.1）→ dial + bind +
  （有 baseDn 时）base scope 读 1 条 → `{success:true, message:"…"}`。
  不缓存连接。
- `connection/connect`：解析 lifecycle params → `Profile`（含策略派生）→
  存连接表 → `{success:true}`。**惰性建连**：首个领域调用才 dial/bind
  （对齐 tiny-rdm withConn 语义），失败自动重连一次。
- `connection/disconnect`：`{connection:{id}}` → 关闭并移除连接表条目。

### 5.2 领域方法（对应 tiny-rdm 19 方法）

| 方法 | tiny-rdm | 请求（除 connectionId 外） | 返回 |
|---|---|---|---|
| `ldap/count` | —（A-LDAP 新增：树徽章精确计数） | `baseDn?`、`filter?` | `{count, truncated?}`（scope=one，上限 5000） |
| `ldap/search` | Search(:356) | `baseDn?`（缺省 profile.base_dn）、`filter`（RFC 4515 校验）、`scope`（base/one/sub）、`attributes?[]`、`sizeLimit?`、`pageSize?`、`typesOnly?`、`derefAliases?`（never/searching/finding/always） | `{entries:[{dn,attributes:{attr:[v…]}}], count, truncated}`（pageSize 走 SearchWithPaging 聚合） |
| `ldap/entry/get` | GetEntry(:426) | `dn`、`attributes?[]` | `{entry:{dn,attributes}}`；屏蔽属性过滤后返回 |
| `ldap/rootDse` | RootDSE(:452) | — | `{attributes:{…}}`；`allowed_base_dns` 非空时禁用（沿袭） |
| `ldap/schema` | GetSchemaMetadata(:472) | `refresh?`（默认走缓存） | `{attributeTypes:[…], objectClasses:[…]}`；缓存落 `cache/schema-<hash>.json` |
| `ldap/entry/add` | AddEntry(:508) | `dn`、`attributes:{attr:[v…]}` | `{success:true}`；写白名单 + 值转义 |
| `ldap/entry/modify` | ModifyEntry(:553) | `dn`、`changes:[{operation:add/replace/delete, attribute, values[]}]` | `{success:true}`；屏蔽属性拒绝修改 |
| `ldap/entry/delete` | DeleteEntry(:606) | `dn` | `{success:true}` |
| `ldap/entry/modifyDn` | ModifyDN(:641) | `dn`、`newRdn`、`newParentDn?`、`deleteOldRdn` | `{success:true}`；新旧 DN 均过写白名单 |
| `ldap/connections/statuses` | ConnectionStatuses(:695) | — | `{statuses:[{connectionId, state:connected/idle/error, lastError?, lastUsedAt}]}` |
| `ldap/presets/list` / `save` / `remove` | SearchPreset（前端 store） | `preset:{id,name,baseDn,filter,scope,attributes,sizeLimit}` 等 | 本地 `presets.json` CRUD |

原 19 方法中 `ListProfiles/SaveProfile/RemoveProfile/ResolveProfile/TestProfile/
Start/Stop` 的归宿：profile CRUD 删除（宿主连接管理）、TestProfile → 
`connection/test`、Start/Stop → 进程生命周期。

### 5.3 事件

LDAP 无流式场景，仅一个：`ldap/audit`（写操作成功/拒绝后发
`{connectionId, action, target, result}`，供工作台轻提示；审计落盘同一条）。

## 6. 安全策略移植（policy.go）

1. **DN 白名单**：读操作要求目标 DN ∈ `allowed_base_dns`（空=不限）；
   写操作（add/modify/delete/modifyDn 目标与来源）∈ `allowed_write_base_dns`
   （空=回退读白名单，再空=不限）。实现 = tiny-rdm `dnWithinBase`(:1925)
   原样 + 单测。
2. **屏蔽属性**：默认表 11 项（userPassword、unicodePwd、objectSid 等，
   ldap_service.go:78-90）；返回/修改请求中出现即从结果剔除 / 拒绝；
   `blocked_attributes` 覆盖默认表。
3. **read_only**：拒绝全部 `ldap/entry/*` 写方法，错误信息与 ssh-sftp
   只读语义一致。
4. **写值转义**：`normalizeLDAPWriteValues`(:1799) 原样。
5. **凭据**：bind_password/ntlm_hash/krb_password 只存内存连接表；
  statuses/prefs/audit/事件均不携带；stderr 日志打印 profile 时先脱敏。

## 7. 前端实施

**工作台与宿主桥（实施前置，先读再写代码）**：

- 连接生命周期由宿主驱动：用户在宿主连接表单保存/连接时，宿主调 sidecar
  的 `connection/test|connect`；工作台打开时连接已就绪，前端经
  `window.dbxPlugin`（Host API）取当前 connection 上下文（workbenchId +
  connection 摘要，宿主 queryStore 补丁已带）——照 ssh-sftp 插件
  `App.vue` 的既有模式实现，不发明新机制。
- LDAP **无 session 概念**（区别于 ssh 的 `ssh/session/open`）：所有
  `ldap/*` 方法只带 `connectionId`；工作台关闭无需 sidecar 调用
  （可发 `workbench/close` 清 UI 态，语义对齐 ssh-sftp）。
- sidecar 调用统一走 `lib/api.ts` 封装：`window.dbxPlugin` 桥 →
  `connectionId` 注入 → 错误走 `showError(cause, "ldap")`。

```
src/
├── App.vue                    # 工作台外壳：布局 + 连接上下文（照 ssh-sftp App.vue 模式）
├── components/
│   ├── DnTree.vue             # 树：scope=one 懒展开 + buildTreeKeywordFilter 远程过滤 + 右键菜单
│   ├── SearchForm.vue         # filter/scope/attrs/sizeLimit/pageSize/typesOnly/deref + 预设选择
│   ├── ResultTable.vue        # 结果表（列 = attributes 并集；分页/排序前端做）
│   ├── EntryEditorDialog.vue  # 查看/编辑/新增（属性行编辑 + LDIF 双向）
│   ├── DeleteEntryDialog.vue / ModifyDnDialog.vue
│   ├── SchemaPanel.vue        # attributeTypes/objectClasses 浏览
│   └── ConnectionsPanel.vue   # 多连接状态（ldap/connections/statuses）
└── lib/
    ├── api.ts                 # sidecar 调用封装（见上，camelCase）
    ├── {ldapFilter,dn,ldif,ldapExporter,baseDn}.ts   # tiny-rdm utils 搬运
    ├── schemaCache.ts         # useLdapSchemaCache 语义（进程内 + sidecar 缓存）
    └── i18n.ts                # 七语
```

- 树/搜索/编辑的交互语义对照 `LdapConsolePage.vue`（8543 行）逐块拆：
  树(§990-1100)、过滤器(§1002)、结果表(AgGrid → 轻量表格组件，无新依赖)、
  导出(LDIF/CSV/JSON)。
- Host API 1.0 全覆盖（无事件依赖、无 fileTransfer 依赖）；导出下载用
  Blob URL 兜底。
- 每个组件的文案进 i18n 七语；`scripts/check_i18n.py` 校验 key 集合一致。

## 8. 测试计划

**单测（不连网）**：`validateLDAPFilter` 合法/非法表、`dnWithinBase` 边界、
屏蔽属性过滤、`normalizeLDAPWriteValues`、LDIF 往返、lifecycle params 解析
（含 secret 落点）、policy 组合（read_only × 白名单）。

**smoke（smoke_test.py，OpenLDAP 容器）**：

| # | 场景 | 断言 |
|---|---|---|
| S1 | initialize → connection/test（simple） | success |
| S2 | connect → search base scope root | entries 非空 |
| S3 | search `(objectClass=*)` sub + pageSize | 分页聚合 = 全量 |
| S4 | entry add/modify/get/delete 往返 | 读回一致 |
| S5 | modifyDn 改 RDN | 新 DN 可查、旧 DN 不存在 |
| S6 | read_only 连接写操作 | -32000 拒绝 |
| S7 | blocked_attributes（userPassword） | 结果中不含 |
| S8 | 白名单外 DN 读 | 拒绝 |
| S9 | disconnect → 再调用 | 报连接不存在 |
| S10 | 非法 filter | 报错文案 |

**集成**：`docker-compose.ldap-test.yml` + `ldap-seed`（搬 tiny-rdm）；
M3 增 `smoke_gssapi_test.py`（`ldap-gssapi-test.yml` + `scripts/ldap-gssapi-integration-test.sh`）。

**对标清单**：`docs/FEATURE_PARITY.zh-CN.md` = tiny-rdm 19 方法 + 8 认证 +
前端能力 × 状态（已移植/改造/不适用+原因），每完成一项更新。

## 9. 里程碑任务分解

### M1（核心，依赖 M0 验收）

| # | 任务 | 依赖 | DoD |
|---|---|---|---|
| L1-1 | 仓库初始化（M0 脚手架复制 + manifest §4 + toml） | M0 | package 出 .dbxp 且宿主可见表单 |
| L1-2 | lifecycle → Profile 解析 + 连接表（惰性 + 重连） | L1-1 | 单测过；S1/S2 过 |
| L1-3 | dial/bind：simple/anonymous/unauthenticated + ldaps/StartTLS + TLS 配置 | L1-2 | S1；ldaps 用容器 636 端口 |
| L1-4 | `ldap/search` + `entry/get` + policy（白名单/屏蔽/只读） | L1-3 | S2/S3/S6/S7/S8/S10 |
| L1-5 | 写方法 add/modify/delete/modifyDn + 审计 + `ldap/audit` 事件 | L1-4 | S4/S5；audit.jsonl 有记录 |
| L1-6 | `rootDse`/`schema` + 缓存；`connections/statuses` | L1-3 | smoke 断言 |
| L1-7 | 前端：DnTree/SearchForm/ResultTable/EntryEditor + 预设 | L1-4/5 | 容器数据全流程可操作；七语齐 |
| L1-8 | 导出（LDIF/CSV/JSON）+ SchemaPanel + ConnectionsPanel | L1-7 | 浏览器验证（截图留档） |

### M3（进阶认证）

| # | 任务 | DoD |
|---|---|---|
| L3-1 | external / digest_md5（go-ldap SASL host 解析移植） | smoke 子场景 |
| L3-2 | ntlm / ntlm_hash（NTLMBindWithHash） | smoke 子场景 |
| L3-3 | ldapgssapi 搬运 + kerberos（临时 krb5.conf、SPN 推导、KDC 88 回退） | `smoke_gssapi_test.py` 全绿 |
| L3-4 | manifest kerberos 字段二级联动 + 前端连接对话框适配 | 宿主表单联动验证 |

### M4（MCP 工具）

`internal/mcp/`：`mcp/tools`（14 个 ldap_* 定义，参数 schema 照 tiny-rdm
tools_ldap.go）、`mcp/call`（lifecycle payload 转发 + connectionId 池化，照
ssh-sftp mcp.rs 语义）、`mcp/settings/get|set`（写白名单策略）；敏感属性
脱敏（ldap_redaction.go 移植）进工具返回。DoD：`scripts/smoke_mcp.py` 同款。

### M5/M6（ADS 追赶）

对账表：`docs/ADS_GAP_ANALYSIS.zh-CN.md`（唯一路线来源，落地一项更新一项）。
M5-a：TLS 字段联动显隐 + 连接字段排版重排；filter 构建器 ≠ 运算符；
UI 测试双轨（vitest 组件测试 + `scripts/ui_test.mjs` 浏览器走查入 test.sh）。
M5-b：NOT 组 UI、`ldap/check` 分级连接检查、搜索历史、LDIF 编辑生效。
M6：LDIF 导入、结果批量操作、新条目 objectClass 模板、二进制属性查看器。

## 10. 风险与备注

- ~~`url` 绑定 `host` 的表单形态待 M0-T6 确认~~ → v0.1.19 已切换为
  `host`/`port`/`tls_mode` 结构化字段（见 §4 字段表与 PROGRESS-A §13/§14）。
- ldaps 自签证书：`tls_verify=false` 走 InsecureSkipVerify（连接级显式配置，
  审计记录一条 warning）。
- SearchWithPaging 聚合上限：`sizeLimit` 缺省 500（tiny-rdm 语义），truncated
  标记返回给前端提示。
