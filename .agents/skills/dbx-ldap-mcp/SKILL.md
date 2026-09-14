---
name: dbx-ldap-mcp
description: 通过 MCP 快速调用 DBX LDAP 工具（mcp__dbx-ldap__*）完成任务：检索目录数据、聚合统计、查看 schema、增删改条目。当用户要求查 LDAP/目录服务数据、统计组织单元或用户、编辑条目属性，或提到 ldap_search_digest/ldap_entry_write 等工具时使用。
---

# LDAP MCP 快速调用（mcp__dbx-ldap__*）

工具定位口诀：**查数据 search_digest，看名字空间 ui_schema，写条目 entry_write（删除必两阶段）**。

## 工具地图（8 个）

| 意图 | 工具 | 要点 |
| --- | --- | --- |
| 查数据 + 聚合 | `ldap_search_digest`（默认 digest 格式） | filter 服务端执行、本地聚合：`stats.subtrees` 子树分布、`groupBy`+`topN`、`distinctAttr` 去重值 |
| 读属性值 | `ldap_search_digest` `attributes:["cn","mail",…]` | **默认投影只有 objectClass**——要读值必须显式给属性列表（rows/cursor 行随之携带） |
| 精确列条目/翻页 | `ldap_search_digest` `format:"rows"` → `ldap_cursor_next` | 每页 n≤20，DN + 投影属性 |
| 查可用的 objectClass/属性 | `ldap_ui_schema` | schema 缓存名单（≤300 截断），拼 filter/属性前先查 |
| 加条目 | `ldap_entry_write` `action:"add"` | 直执行；`attributes` 值一律字符串数组 |
| 改条目 | `ldap_entry_write` `action:"modify"` | 直执行；用 `changes:[{operation,attribute,values}]`，operation ∈ `add`/`replace`/`delete`（精确小写）——`replace` 是整属性替换，改多值属性要给完整新值列表 |
| 删条目 | `ldap_entry_write` `action:"delete"` | 两阶段（preview 带 `childCount`；有子节点配合 `recursive:true`，慎用）；token 60 s/一次性/绑参数 |
| 驱动工作台 | `ldap_ui_focus/search/select/state` | stdio 恒 UNAVAILABLE——改用 search_digest |

## 连接参数（内联必填项）

`host` 必填（裸主机名或完整 ldap/ldaps URL）；`port` 缺省 389/636；
`authType` simple（默认，配 `bindDn`+`password`）/anonymous/unauthenticated；
`baseDn` 缺省检索根；`tlsMode` none/starttls/ldaps（或便捷 `startTls:true`）；
`readOnly:true` 时写工具从清单剔除。

**标准顺序**：① 内联凭据直连（凭据进参数与上下文，仅限本机可信会话；
相同参数池化为 `mcp-<hash>`，后续可只传回池化 id）；② DBX 保存连接 →
传 `connectionId`（应用运行中自动桥转发，凭据不出参数）。**Kerberos 族与
SASL 细化（DIGEST-MD5 等）stdio 不支持**——必须走工作台保存连接 + 桥。

## 硬限制

| 限制 | 值 | 超限动作 |
| --- | --- | --- |
| 单响应 | 16 KiB（`truncated:true`） | rows+cursor、加 scope/收窄 baseDn |
| digest 组数 / topN / 样本 | ≤20 / ≤10 / ≤5 | 设置项只可在上限内调 |
| cursor | n≤20，TTL 10 min | 过期重新 search |
| confirmToken | 60 s，参数一变即作废 | 重新 preview |

## 高频坑

- **filter 里的特殊值**：含 `()` `\` 的值要按 RFC 4515 转义；属性名大小写不敏感但值区分。
- **`objectClass=*` 全库扫**成本高：先 `ldap_ui_schema` 看名字空间，用
  `scope:"singleLevel"` + 精确 `baseDn` 收窄，或直接按 objectClass 过滤。
- **modify 的 operation 是精确小写** `add|replace|delete`；`replace` 整属性替换
  （旧值全没），只想加一个值用 `operation:"add"`。
- **默认投影不含业务属性**：digest 样本/rows 只带 objectClass + DN，读值必须
  传 `attributes` 列表（真机实测 2026-09-14）。
- 空 `matched` 不一定是查错：确认 baseDn/scope；`cursorTruncated:true` 表示物化行数
  被上限截断（默认 1 万），深翻页前先收窄。
- 只读连接（`readOnly:true`）写工具直接缺席/被拒——找连接属主要求可写副本。

更多背景（配置、桥接兜底、契约细节）见 skill `dbx-mcp-usage` 与
`shared/TEST_PLAN_MCP_ZCODE.zh-CN.md`。
