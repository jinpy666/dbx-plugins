# dbx-ldap-plugin 可实施文档

> 公共基线：`IMPL_PLAN_M0_COMMON.zh-CN.md`（SDK/打包/lifecycle/审计/测试基建，下称 M0 文档）。
> 插件：`io.dbx.ldap`，仓库 `~/btroot/dbx-plugins/ldap`，sidecar `dbx-plugin-ldap`（Go）。
> 能力来源：初版迁移自已退役的外部参照实现（file:line 见迁移映射表）。

## 0. 目标与非目标

**目标**：对齐参照基线 LDAP Console 全部能力——19 个服务方法语义、8 种认证、
DN 树/搜索/条目编辑/LDIF/CSV/预设前端、DN 白名单与屏蔽属性安全策略、
schema 元数据、（M4）14 个 MCP 工具。

**非目标**（方案 D3/D5，DBX 语义替代）：
- SQLite profile 存储 → 宿主 connection-provider；
- SSH 隧道/代理/endpoint rewrite → DBX 传输层（拨 `runtime.host:port`）；
- `ldapi://` Unix socket（Phase 2 视宿主需求）；password modify / WhoAmI /
  Compare 扩展操作（参照基线亦无，不对齐不缺失。PLA 对标的密码能力走
  **客户端哈希编码 + bind 校验**实现，不新增扩展操作，见
  `PLA_GAP_ANALYSIS.zh-CN.md` N2）。

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
│       ├── ldapconn/                   # 领域层（初版自外部参照移植，见 §3）
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
| `github.com/go-ldap/ldap/v3` | v3.4.13（对齐参照基线） | 协议、SearchWithPaging、GSSAPI/NTLM/Digest 绑定 |
| `github.com/jcmturner/gokrb5/v8` | v8.4.4（对齐） | Kerberos 客户端（M3） |
| `github.com/google/uuid` | latest | operationId/连接 id 兜底 |
| dbx-plugin-sdk（Go） | CLI 捆绑版本 | M0 文档 §1.3 |

无 cgo（gokrb5 纯 Go）；`CGO_ENABLED=0` 交叉编译。

## 3. 初版代码迁移映射（外部参照 → 本插件，历史）

| 参照基线（已退役） | 去处 | 改造点 |
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
  （含 withConn 缓存与自动重连语义），失败自动重连一次。
- `connection/disconnect`：`{connection:{id}}` → 关闭并移除连接表条目。

### 5.2 领域方法（19 个领域方法）

| 方法 | 参照基线（已退役） | 请求（除 connectionId 外） | 返回 |
|---|---|---|---|
| `ldap/count` | —（A-LDAP 新增：树徽章精确计数） | `baseDn?`、`filter?` | `{count, truncated?}`（scope=one，上限 5000） |
| `ldap/search` | Search(:356) | `baseDn?`（缺省 profile.base_dn）、`filter`（RFC 4515 校验）、`scope`（base/one/sub）、`attributes?[]`、`sizeLimit?`、`pageSize?`、`typesOnly?`、`derefAliases?`（never/searching/finding/always） | `{entries:[{dn,attributes:{attr:[v…]}}], count, truncated}`（pageSize 走 SearchWithPaging 聚合） |
| `ldap/entry/get` | GetEntry(:426) | `dn`、`attributes?[]` | `{entry:{dn,attributes}}`；屏蔽属性过滤后返回 |
| `ldap/rootDse` | RootDSE(:452) | `attributes?[]`；前端导出默认显式请求 `["*", "+"]`，含操作属性 | `{attributes:{…}}`；`allowed_base_dns` 非空时禁用（沿袭） |
| `ldap/schema` | GetSchemaMetadata(:472) | `refresh?`（默认走缓存） | `{attributeTypes:[…], objectClasses:[…]}`；缓存落 `cache/schema-<hash>.json` |
| `ldap/entry/add` | AddEntry(:508) | `dn`、`attributes:{attr:[v…]}` | `{success:true}`；写白名单 + 值转义 |
| `ldap/entry/modify` | ModifyEntry(:553) | `dn`、`changes:[{operation:add/replace/delete, attribute, values[]}]` | `{success:true}`；屏蔽属性拒绝修改 |
| `ldap/entry/delete` | DeleteEntry(:606) | `dn`、`recursive?`（缺省 false 单条语义；true 删整棵子树：优先 Tree Delete 控件 `1.2.840.113556.1.4.805`，服务端不支持（unavailableCriticalExtension/unavailable/unwillingToPerform）回退自底向上逐条删除，条目上限 1000 超限报错不删；写白名单只校验目标 DN） | `{success:true}`；recursive 审计聚合为一条 `subtree_delete`（含 `deletedCount`） |
| `ldap/entry/childrenCount` | —（N1 新增：删除确认子条目计数） | `dn` | `{count, truncated?}`（scope=one、filter `(objectClass=*)`、上限 5000，风格同 `ldap/count`）；白名单约束同读操作 |
| `ldap/entry/modifyDn` | ModifyDN(:641) | `dn`、`newRdn`、`newSuperior?`（界面“新父 DN”）、`deleteOldRdn` | `{success:true}`；新旧 DN 均过写白名单 |
| `ldap/connections/statuses` | ConnectionStatuses(:695) | — | `{statuses:[{connectionId, status:connected/idle/error, readOnly?, lastError?, lastUsedAt}]}` |
| `ldap/presets/list` | LDAPSearchPreset（sidecar store） | — | `{presets:[...]}`；本地 `presets.json`，不含凭据 |
| `ldap/presets/save` | LDAPSearchPreset | `preset:{id,name,baseDn?,filter?,scope?,attributes?,sizeLimit?}`；id 空则生成，name 必填 | `{success:true,preset}`；按 id 更新。仅持久化过滤器串，前端应用时重建条件树；不存 `conditions` |
| `ldap/presets/remove` | LDAPSearchPreset | `id` | `{success:true}`；id 不存在报业务错误，不返回整表 |

原 19 方法中 `ListProfiles/SaveProfile/RemoveProfile/ResolveProfile/TestProfile/
Start/Stop` 的归宿：profile CRUD 删除（宿主连接管理）、TestProfile → 
`connection/test`、Start/Stop → 进程生命周期。

### 5.3 事件

LDAP 无流式场景，仅一个：`ldap/audit`（写操作成功/拒绝后发
`{connectionId, action, target, result}`，供工作台轻提示；审计落盘同一条）。

### 5.4 条目关联视图（对标 ADUC Members / Member Of，纯前端）

对标 ADUC（Active Directory 用户和计算机）的 Members / Member Of 语义，
为条目编辑器新增第三个标签页「关联」+ 树右键菜单「成员」（以关联模式
打开编辑器）。**接口层面：无新增协议方法、无后端改动**——纯前端组合既有
`ldap/search` + `ldap/entry/get` 数据完成双向关联，协议文档无需变更。

- **成员（Members）**：直读条目自身 `member`（回退 `uniqueMember`）属性值
  （`ldap/entry/get`；`member` 不在屏蔽属性默认表），渲染为可点击 DN 列表
  （点击=打开该条目），虚拟滚动（VirtualList）承载大组。
- **所属（Member Of）**：前端组合既有 `ldap/search`，以连接 Base DN 为
  base、`scope=sub`、过滤器 `(member=<本条目DN>)`（值经既有
  `escapeLdapFilterValue` 做 RFC 4515 转义）、`attributes=["1.1"]`（只取
  DN）、`sizeLimit=1000`，反查引用此 DN 的条目。
- **规避屏蔽属性策略**：`memberOf` 在默认屏蔽表（policy.go
  defaultLDAPBlockedAttributes），但本设计只把 member/memberOf 用作
  **过滤器**（过滤器不做属性过滤），Member Of 反查不需要读出任何条目的
  `memberOf` 输出，因此不受屏蔽属性影响。
- **已知限制**（前端提示文案同步）：AD 主组（primaryGroupID）成员关系不走
  member/memberOf，不解析；嵌套组成员展开需
  LDAP_MATCHING_RULE_IN_CHAIN（1.2.840.113556.1.4.1941），不支持，仅直接
  成员；Member Of 反查范围限定在连接 Base DN 子树内；OpenLDAP 未启用
  memberof overlay 不影响本设计（不依赖 `memberOf` 属性）。
- **UI/i18n**：新组件 `AssociationPanel.vue`；新增 i18n 键
  `editor.assocMode` 与 `associations.*`（七语）。

### 5.4.1 通用 DN 引用泛化（正查 DN 引用区 + 反查被引用区，L5-5～L5-7）

§5.4 落地 member/memberOf 专属版后泛化为通用 DN 引用能力：条目上除
member/uniqueMember 外的任意 DN 值属性（对标 ADUC 中可点击的
managedBy/owner/seeAlso 等）同样进关联视图。仍无新增协议方法、无后端改动。

- **正查（DN 引用区）**：条目上除 member/uniqueMember 外的 DN 值属性
  （managedBy/owner/secretary/seeAlso/manager/assistant/altRecipient 等），
  值按属性分组、可点击打开目标条目（交互语义同 §5.4 Members 列表）。
- **DN 属性识别（schema 驱动 + 兜底）**：解析 `ldap/schema` 的
  attributeTypes——SYNTAX=1.3.6.1.4.1.1466.115.121.1.12（DN）或
  1.3.6.1.4.1.1466.115.121.1.34（nameAndOptionalUID），或定义含
  `SUP distinguishedName`/`SUP nameAndOptionalUID`（单层启发，不递归父级）；
  schema 不可用（rootDse/schema 请求失败或解析不出可用结果）时静默降级，
  回退内置核心表 DN_REFERENCE_CORE=["managedBy","owner","secretary",
  "assistant","manager","seeAlso","altRecipient"]。
- **反查（被引用区）**：单次 OR 过滤器
  `(|(managedBy=<本条目DN>)(owner=<本条目DN>)…)`（值经既有
  `escapeLdapFilterValue` 做 RFC 4515 转义；核心表收敛到单属性时退化为
  普通过滤器）。**反查只用内置核心表，不用 schema 全量 DN 属性**——后者
  可能数百个，避免巨型 OR；scope=sub、base=连接 Base DN、
  attributes=["1.1"]、sizeLimit=1000，语义同 §5.4 Member Of。
- **实现落点**：新 `frontend/src/lib/dnAttributes.ts`
  （deriveDnValuedAttributes / buildReferencedByFilter / DN_REFERENCE_CORE，
  纯函数 + 单测）；AssociationPanel 扩展 DN 引用/被引用两区；App→
  EntryEditorDialog→panel 贯通 `dnAttributes` prop（schema 惰性加载，
  失败静默降级兜底表）。
- **已知限制**（前端提示文案同步）：反查覆盖面限定核心表——schema 中其余
  DN 属性正查可见、反查不覆盖；AD 主组/嵌套组限制沿袭 §5.4。
- **交互形态（UX 复审后定稿）**：关联视图在编辑器内以**子页签**呈现——
  「成员 / 所属 / DN 引用 / 被引用」四页签互斥（带计数徽标，搜索类加载完
  才出数字），一次只显示一个列表，不再纵向堆叠；列表顶部常驻**本地过滤框**
  （对激活列表做 DN 大小写不敏感子串过滤，dn 变化即清空）；行统一两行式
  （首行 RDN + 来源属性小字，次行完整 DN），VirtualList 虚拟化承载大列表
  （resetKey 纳入 dn/页签/过滤词）。**树右键「成员」语义 = 立即组织
  `(memberOf=<节点DN>)` 过滤器并在右侧结果区执行搜索**（SearchForm 新暴露
  `runFilterAt(baseDn, filter)`，源码模式承载过滤器、Base=连接 Base DN、
  scope=sub，用户可在结果区继续改条件/分页/导出）；服务器无 memberof
  （如未启用 overlay 的 OpenLDAP）时该搜索为空，组内成员仍以编辑器关联
  页签的成员区（member 属性直读）为准。

## 6. 安全策略移植（policy.go）

1. **DN 白名单**：读操作要求目标 DN ∈ `allowed_base_dns`（空=不限）；
   写操作（add/modify/delete/modifyDn 目标与来源）∈ `allowed_write_base_dns`
   （空=回退读白名单，再空=不限）。实现 = `dnWithinBase`
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
    ├── {ldapFilter,dn,ldif,ldapExporter,baseDn}.ts   # 初版自外部参照 utils 搬运
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
| S5 | modifyDn 改 RDN + 子树移动 | 新 DN 可查、旧 DN 不存在；`newSuperior` 移动子树，`deleteOldRdn` 两态的命名属性值正确 |
| S6 | read_only 连接写操作 | -32000 拒绝 |
| S7 | blocked_attributes（userPassword） | 结果中不含 |
| S8 | 白名单外 DN 读 | 拒绝 |
| S9 | disconnect → 再调用 | 报连接不存在 |
| S10 | 非法 filter（括号、缺失比较符、非法十六进制转义） | 拒绝并给出 filter 错误 |
| S17 | UTF-8/字面星号过滤、RFC2307 数值比较、计数 | 转义匹配与数值顺序正确；超过 JS 安全整数的相邻值不混淆；转义逗号子条目计数一致 |
| S18 | 别名四模式 × base/one/sub 范围 | 查找 base 与搜索阶段解引用区分；同目标去重、祖先别名解析、循环别名拒绝 |

> S11–S16 为后续追加场景（rootDse namingContexts、{SSHA} 密码写 + bind 校验、
> childrenCount + 递归子树删除、jpegPhoto 二进制、条目关联 member/memberOf
> 反查、通用 DN 引用 managedBy 往返），随任务落地，清单以
> `scripts/smoke_test.py` 头部为准。
> S17/S18 为 review 第 7 轮追加的真实容器契约回归；mock 对实际别名解引用
> 和未支持的 schema matching rules 仍明确报不支持，不把这些 smoke 当作
> mock 已实现完整 LDAP 语义的证明。

**集成**：`docker-compose.ldap-test.yml` + `ldap-seed`（初版改造自参照基线种子）；
M3 增 `smoke_gssapi_test.py`（`ldap-gssapi-test.yml` + `scripts/ldap-gssapi-integration-test.sh`）。

**能力清单**：`docs/FEATURE_PARITY.zh-CN.md` = 参照基线 19 方法 + 8 认证 +
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

> **状态（2026-09-12，M1 已落地）**：按 `shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md`
> v2 实施，取代本节初版"14 个工具"规划（设计 v2 改为分层 8 工具：UI 驱动
> 4 + 本地读 2 + 元发现 1 + 写 1）。协议与两阶段语义见
> `docs/MCP.zh-CN.md`。

`internal/mcp/`：`mcp/tools`（8 个 ldap_* 定义 + JSON Schema，只读连接不进
写工具清单）、`mcp/call`（lifecycle payload 注册 + 工具分派，照 ssh-sftp
mcp.rs 语义移植为 Go）、`mcp/settings/get|set`（digest 参数、截断宽度、
report 等待时长、响应上限；`mcp-settings.json` 持久化）；UI intent 通道
（事件 `ldap/ui/intent` + 方法 `ldap/ui/state/report` + 前端
`shared/frontend/uiIntent.ts`）、`ldap_search_digest`/`ldap_cursor_next`
本地读、`ldap_entry_write` 两阶段写（delete/modifyDn 强制，审计
`source:"mcp"`）。DoD：`scripts/smoke_mcp.py`（M1–M10，未注册 SKIP）+
intent/digest/cursor/confirmToken 单测（`internal/mcp/*_test.go`，用例
清单进 shared/frontend/README）——已达成。

### M5/M6/M7（ADS + PLA 双标追赶）

对账表：`docs/ADS_GAP_ANALYSIS.zh-CN.md`（ADS 侧唯一路线来源）+
`docs/PLA_GAP_ANALYSIS.zh-CN.md`（PLA 侧唯一路线来源，含任务分解 L6-x 与
smoke S12–S14，S11 为先期 rootDse 场景）；两表并轨同一框架，落地一项更新一项。
M5-a：TLS 字段联动显隐 + 连接字段排版重排；filter 构建器 ≠ 运算符；
UI 测试双轨（vitest 组件测试 + `scripts/ui_test.mjs` 浏览器走查入 test.sh）。
M5-b：NOT 组 UI、`ldap/check` 分级连接检查、搜索历史、LDIF 编辑生效。
M6（扩容后）：LDIF 导入、结果批量操作、模板化新建（N4）、二进制查看+
上传（N3）、子树删除（N1）、密码哈希辅助（N2，提级）。
M7（按需）：mTLS、CRAM-MD5、referral 策略、服务器端排序、digest realm、
DSML、schema 语法/匹配规则透出、uid 定位 DN、uid/gid 自动编号、属性显示
排序、datetime 统一格式化（两对账表 P2 汇总）。

### 新特性追加：条目关联视图（对标 ADUC Members / Member Of，任务编号 L5-x）

设计定稿见 §5.4 与 §5.4.1（无新增协议方法、无后端改动，纯前端组合）。任务
编号沿用 L 系列顺延取 L5（L6-x 已归 PLA 对账表 §7 的 N1–N4）。

| # | 任务 | 依赖 | DoD |
|---|---|---|---|
| L5-1 | `AssociationPanel.vue`：Members 直读 member（回退 uniqueMember，VirtualList 可点击 DN）+ Member Of 组合 `ldap/search` 反查（escapeLdapFilterValue、attributes=["1.1"]、sizeLimit=1000） | L1-7 | 组件单测过；容器种子数据下成员/所属互跳正确 |
| L5-2 | 入口集成：条目编辑器第三个标签页 + 树右键菜单「成员」（以关联模式打开编辑器） | L5-1 | 浏览器走查（截图留档）；大组 VirtualList 渲染正常 |
| L5-3 | i18n 七语：`editor.assocMode`、`associations.*` + 已知限制提示文案 | L5-1 | `scripts/check_i18n.py` 过（key 集合一致） |
| L5-4 | smoke S15：临时组 member 反查往返 + memberOf 探测（支持性不足记 SKIP 说明）+ 删除后不复现断言 | L5-1 | 完成定义四件套收口：单测 + smoke S15 + FEATURE_PARITY/任务清单更新 + 七语文案 |
| L5-5 | `frontend/src/lib/dnAttributes.ts`：deriveDnValuedAttributes（schema attributeTypes 按 SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 / 1.3.6.1.4.1.1466.115.121.1.34 或 SUP distinguishedName / nameAndOptionalUID 单层启发识别 DN 值属性）+ buildReferencedByFilter（核心表 OR 过滤器、RFC 4515 转义、单属性退化）+ DN_REFERENCE_CORE 兜底表 | L5-1 | 纯函数单测过（schema 可用/缺失两态） |
| L5-6 | AssociationPanel 扩展「DN 引用区 / 被引用区」+ App→EntryEditorDialog→panel 贯通 `dnAttributes` prop（schema 惰性加载、失败静默降级兜底表） | L5-5 | 组件单测过；容器下 managedBy/owner 等正查按属性分组可点击、反查命中；浏览器走查（截图留档） |
| L5-7 | smoke S16：generic DN reference (managedBy) 往返（临时 OU 挂 managedBy→反查命中→删除后不复现；服务器 schema 无 managedBy 记 SKIP 说明） | L5-5 | 完成定义四件套收口：单测 + smoke S16 + FEATURE_PARITY/任务清单更新 + 七语文案 |

### Review 第 4 轮（2026-09-12，fresh review）

本轮按用户指定的空/加载/错误态、输入校验、a11y、大目录性能、mock/真实桥
五个面复核；实施四组小改动，不改后端或宿主，不重复 round1–3 已修项。

| 任务 | 内容 | 状态与验收 |
|---|---|---|
| R4-1 | 对齐当前宿主 `onContext` 与 `onEvent` 的 `env` 消息；保留旧回调降级，类型与 mock 同步 | [x] App 回归 + 从当前宿主源码生成 SDK 的 iframe 消息验证通过 |
| R4-2 | 搜索数值完整校验、运行/预设/树快捷入口门禁；源码与数值错误关联到输入框 | [x] 组件回归 + 浏览器实际输入/恢复通过；复用七语错误文案 |
| R4-3 | 树过滤原地重试、刷新当前视图、加载/错误播报；懒节点与过滤项 aria、左右键展开/折叠 | [x] 组件回归 + 浏览器重试/键盘操作通过；新增 `tree.retry` 七语齐全 |
| R4-4 | mock 补齐 `ldap/entry/childrenCount` 的 `{count,truncated}` 与空 DN 拒绝 | [x] mock 回归通过；真实容器 S13 通过 |
| R4-5 | 万级 OU 渲染和续载评估（仅评估） | [x] 10000 条 fixture：树 39 个 DOM 行、表格 50 行；确认树到 5000 条后失去续载入口，未改分页/截断语义 |

本轮未收敛：EntryEditor 的 objectClass/MUST 与 DN 预检、搜索/详情状态、
预设 save/remove 的真实回包与 mock 偏差、剩余树键盘操作列入下一轮。
完整发现与验证见工作区 `.goal-state/report-ldap-round4.md`，交付记录见
`PROGRESS-P-LDAP.zh-CN.md`「review 第 4 轮」。

### Review 第 5 轮（2026-09-12，round4 P2 跟进）

沿用 LDAP 目录内小改动边界；工作来源为 R4-05/06/07/08，不重复前轮已修项。

| 任务 | 内容 | 状态与验收 |
|---|---|---|
| R5-1 / R4-08 | 预设 save/remove 类型、mock 与 UI 同步真实回包；按返回 id 更新，过滤器串为恢复依据 | [x] 组件/mock/浏览器通过；真实 sidecar 保存、重启读取、更新与删除回包验证通过 |
| R5-2 / R4-05 | objectClass → MUST 即时提示/定位/保存门禁；现存不可读属性与隐藏凭据容错 | [x] MUST/SUP、隐藏属性容错与浏览器缺失字段定位通过；七语齐全 |
| R5-3 / R4-06 | RDN 空段、转义、引号配对检查；LDIF 解析后校验完整 DN | [x] DN/编辑器回归与浏览器改名输入验证通过；前轮 LDIF 离页守卫保持 |
| R5-4 / R4-07 | 搜索与条目详情 loading/error/empty、原地重试；旧请求失效与关闭保护 | [x] 在途/失败/成功空态与关闭/切换连接回归通过，浏览器重试通过；七语齐全 |

R4-09 大目录保持仅评估；R4-10 树剩余键盘操作、R4-11 mock 搜索/修改保真度
继续留后续轮次；前轮人工/真机与维持项不变。
额外记录 P2：`scripts/smoke_test.py` 的 scenario 装饰器在成功时执行场景两次，
本轮用纯计数器复现后保留待修。完整验证与收敛判定见工作区
`.goal-state/report-ldap-round5.md`。

### Review 第 6 轮（2026-09-12，剩余 P2 跟进）

用户继续后，从 R4-10、R4-11、R5-05 出发实施四组小修；无后端产品代码、
公共层或宿主改动，复用既有七语文案，无新增依赖。

| 任务 | 内容 | 状态与验收 |
|---|---|---|
| R6-1 / R4-10 | 树 Home/End、父子导航、跨虚拟窗口的上下键；原生“加载更多”按钮可 Tab/Enter 操作并保持加载期焦点 | [x] 真实 VirtualList 组件回归与浏览器按键通过；保留分页/虚拟化策略 |
| R6-2 / R4-11 读取 | mock 属性选择、typesOnly、成功分页聚合、未知方法失败；RootDSE 操作属性请求/投影一致 | [x] mock 与浏览器导出通过；真实容器属性投影/类型值形状已对照。修复 RootDSE 默认导出遗漏操作属性 |
| R6-3 / R4-11 写入 | 前端移动字段对齐 Go 的 newSuperior；mock 按值增删、修改失败原子性、RDN 值与子树迁移 | [x] API→mock 回归、浏览器移动与真实容器 S5 通过；修复新发现的“新父 DN 被静默忽略” |
| R6-4 / R5-05 | smoke 装饰器成功场景只执行一次；离线回归进入 test.sh，扩展 S5 | [x] Python 3 测试（含 6 个异常/SKIP 子例）、容器 17/17 通过 |

全套 `scripts/test.sh` 通过：前端 **37 文件 / 554 用例**、UI **20/20**、Go、
打包成功（当前并发更新后的 manifest 为 0.1.58，本轮未修改它）。
独立 `go vet ./... && go test -count=1 ./...` 通过。

仍未收敛：真实 OpenLDAP 的 pageSize/sizeLimit 组合可返回 LDAP Code 4，例如
7 条目录下 2/3、3/3、10/3 失败，而 1/3、2/4 可聚合返回。后端与 truncated
语义保持不动；mock 只镜像成功聚合，不模拟这些服务器控制错误。alias 解引用
遇到 alias fixture 时明确不支持；完整 matching rules、扩展控制/认证矩阵等
仍需专项契约夹具。R4-09 大目录继续只评估，前轮人工/真机与维持项不变。
完整报告与失败原始日志见 `.goal-state/report-ldap-round6.md`。

### Review 第 7 轮（2026-09-12，过滤器与 mock 契约跟进）

从 R4-11 遗留的过滤器/别名面出发实施三组小修；新增 P2 源码过滤器校验缺口
一并处理。只改 LDAP 源码/测试/文档，未改 Go 产品代码、manifest、宿主与
公共层，无新增依赖，复用七语 `search.filterInvalid`。

| 任务 | 内容 | 状态与验收 |
|---|---|---|
| R7-1 / 表单预检 | 拒绝仅括号平衡但缺属性/比较符/合法转义的源码过滤器；保留空断言、选项/OID、多段子串和扩展匹配合法形态 | [x] 纯解析回归 + 七语字段关联/门禁/纠正恢复 + 浏览器通过 |
| R7-2 / R4-11 过滤与计数 | mock 复用 UTF-8 解码；uidNumber/gidNumber 用 BigInt 比较；未支持的匹配规则预先拒绝；count 复用解析和直接子条目判断 | [x] UTF-8/字面星号、数值精度、布尔短路、转义逗号 DN 回归通过；真实 S17 通过 |
| R7-3 / R4-11 别名边界 | 仅在 base/祖先或实际搜索范围需要解引用时拒绝；无关 alias 不再让普通搜索失败 | [x] mock 有效范围回归通过；真实 S18 四态/范围矩阵、去重与循环拒绝通过。完整 mock 解引用引擎仍保留 |

验证：前端 **37 文件 / 608 用例**（+54），UI **21/21**，Go vet 与
`go test -count=1 ./...` 通过，Docker **19/19**（S15/S16 部分断言继续条件
跳过），全套 `scripts/test.sh` 与当前 **0.1.58** 打包通过。首次新增回归
43 处失败复现缺陷；首次扩展容器为 18/19，因 S17 断言未容忍 count 响应
省略 `truncated:false`，修正测试后通过，未改截断语义。

**未收敛**：完整 matching rules、别名解引用与服务器控制错误仍需独立夹具；
R6-03 pageSize/sizeLimit 交互保留后端专项。大目录继续仅评估，其他指定
维持项与真机/GUI e2e 项不变。完整证据见 `.goal-state/report-ldap-round7.md`。

## 10. 风险与备注

- ~~`url` 绑定 `host` 的表单形态待 M0-T6 确认~~ → v0.1.19 已切换为
  `host`/`port`/`tls_mode` 结构化字段（见 §4 字段表与 PROGRESS-A §13/§14）。
- ldaps 自签证书：`tls_verify=false` 走 InsecureSkipVerify（连接级显式配置，
  审计记录一条 warning）。
- SearchWithPaging 聚合上限：`sizeLimit` 缺省 500（沿袭初版语义），truncated
  标记返回给前端提示。
