# DBX 能力迁移评估（聚焦版，2026-08-28 定稿）

> **本版结论（用户决策）**：放弃 `io.dbx.mcp-hub` 设想；playbook/trigger/
> 审批模式包/LLM 顾问等一切新插件与新包**全部不做**。迁移范围收敛为
> **三个插件：io.dbx.ssh（已存在，继续增强）、io.dbx.ldap、
> io.dbx.files**。本文是最终盘点存档：§1 三插件增强 backlog（唯一的
> 后续工作来源）、§2 不迁移清单（含可选的宿主反馈建议）、§3 依据存档。
>
> **2026-09-05 更新**：经用户决策新增第四插件 `io.dbx.kafka`（本文 §2 的
> "kafka 控制台不迁移"结论随之解除），实施见
> `kafka/docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md`；本文其余内容保持当时决策存档原样。

## 1. 三插件增强 backlog（后续工作的唯一来源）

### 1.1 io.dbx.ssh（已存在）

| 项 | 内容 | 状态/前置 |
|---|---|---|
| batch-send（多终端批量命令广播） | `useBatchSend.js`（201 行）+ SshBatchSendPanel：命令广播到多终端（active/visible/all-connected 三档、appendNewline、历史去重回填、Cmd/Ctrl+Enter）；纯前端 + 既有 `ssh/terminal/in/{sessionId}` 通道，无新 sidecar 方法 | **已被 ssh-sftp 仓评估为架构级**：插件当前是单 workbench 单连接上下文，多终端目标集需先与宿主确认多会话语义（多 tab/分屏）——挂起，待宿主多会话 API 明确 |
| 终端输出/会话体验类 | osc633 命令标记解析、session-status、terminal-output 处理 | 已有对标清单跟踪（ssh-sftp 仓 docs/FEATURE_PARITY.zh-CN.md），按既有节奏消化，非本轮新增 |
| 其余 | —— | ssh-sftp 仓对标清单（docs/FEATURE_PARITY*.zh-CN.md）即唯一 backlog，无新增 |

### 1.2 io.dbx.ldap（实施文档 `IMPL_PLAN_DBX_LDAP.zh-CN.md`）

- 全量范围即 M1（simple/anonymous + 浏览/搜索/条目编辑/安全策略）→
  M3（kerberos/ntlm/ntlm_hash/digest_md5/external/unauthenticated）→
  M4（MCP 工具 + 收尾）。**无范围外新增**。
- 唯一外部依赖项：`url` 绑定 `host` 的宿主表单形态（M0-T6 确认，备份方案已备）。

### 1.3 io.dbx.files（实施文档 `IMPL_PLAN_DBX_FILES.zh-CN.md` v3）

- 全量范围即 M2（Rust sidecar + OpenDAL：fs/s3/webdav/ftp + 透传 +
  浏览/读写/传输槽）→ M3（sftp + via-dbx-ssh + 云盘模板 + 历史持久化）→
  M4（MCP 工具）。**无范围外新增**。
- 外部依赖项：宿主隧道 API（via-dbx-ssh，M0-T6 确认，不可用则文档化降级）。

### 1.4 明确不做（本轮砍掉，存档防再议）

| 砍掉项 | 原设想 | 不做的理由 |
|---|---|---|
| `io.dbx.mcp-hub` 插件 | 外部 MCP endpoint 聚合网关（策略/脱敏/包装/审计） | 用户决策放弃；且与「放大三插件」的聚焦目标不一致 |
| playbook + runs 编排插件 | 元数据编排 + Run 历史 | 沙箱 C1（插件间隔离）使执行器无法驱动其他插件；外部参照实现的编排引擎本身半成品（db_call stub、approve_gate 是断言） |
| trigger（cron + webhook） | 计划任务/webhook 网关 | webhook 被 C2（无监听权限）掐死；cron 需常驻执行目标，随 playbook 一并不做 |
| 审批模式包 + LLM 顾问 | 跨插件规则引擎/验证码/LLM 风险评估 | 用户决策砍掉；三插件各自保留**已有的**「危险操作确认对话框 + audit.jsonl」语义（M4 落地），不再扩展 |

## 2. 不迁移清单（及可选的宿主反馈建议）

| 能力 | 不迁移原因 | 给宿主的建议（可选，不影响本路线） |
|---|---|---|
| kafka 控制台（Topics/消费/生产/组/ACL/Schema Registry/监控） | 宿主 `mq.yaml` 已含 kafka/rocketmq/rabbitmq driverProfiles | supportLevel 仅 `connect`；深度控制台值得宿主原生补 |
| structure_sync（schema diff + DDL 向导） | 沙箱 C3（插件无法访问宿主内置连接）+ 方言仅 MySQL/PG（`schema_diff.go:100-107` 非 PG 全回落 MySQL） | 结构同步是数据库客户端本职，建议宿主原生做多方言版 |
| monitor / pubsub / Redis REPL（cli_service） | Redis 专属；宿主 `redis.yaml` 内置 | — |
| database_console / script_runtime / tools | 与 DBX 核心（查询控制台）重叠；tools 与 files 插件重叠 | — |
| resource（统一资源树/标签/镜像） | DBX 已有连接树 | 标签/置顶/从 profile 镜像资源的模式可借鉴进宿主连接管理 |
| port_forward / tcp_route / transport_runtime | 纯传输层；宿主隧道已有（方案 D5） | `CommonProxyRef`「代理 profile 引用」模型值得宿主隧道层吸收 |
| cli_v1 REST 门面 + mcpctl CLI | 整应用外壳，不可移植 | 「工具注册表 + discover 自描述」模式可供宿主 MCP 桥参考 |
| ga / system / preferences / window / update / secrets / vault | 遥测/应用壳/凭据管理（宿主 secret binding 已覆盖） | — |
| MCP 控制台（服务端半，~120 内部工具 runtime） | 宿主 MCP 桥（@dbx-app/mcp-server + dbx_call_plugin_tool）已承担该方向 | — |

## 3. 判定依据存档（沙箱三硬约束 + 调研要点）

**DBX 插件沙箱三硬约束**（多个判定的根据）：

| # | 约束 | 影响 |
|---|---|---|
| C1 | 插件间隔离：sidecar 独立进程，无插件间调用 API | playbook/mcp-hub 类编排或聚合能力无法驱动其他插件 |
| C2 | 无网络监听权限（permissions 仅 `host.*`） | webhook 网关不可行 |
| C3 | 插件无法访问宿主内置连接（lifecycle 只携带本插件连接） | structure_sync 无法复用用户已有 DBX 数据库连接 |

**调研依据（file:line 摘要）**：

- mcp 控制台：`mcp_console_service.go`（2345 行，~28 方法）、
  `endpoint_proxy.go:131-205`；服务端半 `mcp/runtime.go`（18089 本机 server，
  ~120 工具）——后者本就与宿主 MCP 桥重复。
- approval：`approval.go:310`（同步 Require 门）、`critical_approval.go:26-95`
  （三层规则）、`approval_llm.go:30-106`（LLM 顾问，langchaingo 适配器为占位）。
- playbook：`types/playbook.go:107-126`（模型）、`structured_runners.go:12-17`
  （仅 4 runner；db_call stub :220-223）、`structured_runtime.go:88-128`。
- trigger：`trigger_service.go:491`（租约）、`:279,709`（webhook 防重放）。
- structure_sync：`structure_sync_service.go:48/92`（仅 2 方法）、
  `db/sync/`（~1300 行 diff 引擎）、前端向导 2204 行。
- batch-send：`useBatchSend.js`（201 行）、`batch-send.js`、SshBatchSendPanel.vue；
  ssh-sftp 仓评估记录（原批次对标文档已随批次退役删除）。
- DBX 宿主：`plugins/connection-types/`（80+ yaml；`mq.yaml` 含
  kafka/rocketmq/rabbitmq，supportLevel: connect）、`manifest.schema.json`
  permissions 枚举。
