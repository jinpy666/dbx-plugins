# MCP × ZCode 测试方案与缺口登记

状态：2026-09-14 沉淀。背景：files/ldap/kafka 三插件 MCP（`--mcp` stdio）接入
ZCode 后的「真实 agent 调用测试」暴露了一类此前所有离线设施都未拦截的问题
（严格宿主形状校验拒收，见 §4 G1），据此把测试体系整理为五层，并登记缺口与
处置。同构契约见 `shared/MCP_ACCEPTANCE.zh-CN.md`；agent 使用指引见 skill
`dbx-mcp-usage`。**改动触及 MCP 面（schema/错误形状/工具清单/协议层）时，
按 §6 回归矩阵执行；新插件接入 ZCode 按 §5 清单走全流程。**

## 1. 分层测试矩阵

| 层 | 目的 | 资产 | 执行入口 |
| --- | --- | --- | --- |
| **L0 单测红线** | sidecar 内纯逻辑：错误档位、两阶段 token、只读/删除门、参数容错、cursor TTL、schema 形状红线 | files `cargo test`（222，mcp 模块含 stdio isError/内联池化/TTL/根拒绝）；ldap/kafka `go test ./...`（含 `tools_schema_test.go` required:null 红线、writegate/tolerance 矩阵） | 插件 `backend/` 内 `cargo test` / `go test ./...` |
| **L1 插件 smoke** | 无 UI 全链路场景（离线 + 容器 + stdio + 桥 mock），SKIP 语义不假 FAIL | `files/scripts/smoke_mcp.py`（31 场景）、`ldap`（19）、`kafka`（18）、`ssh` 同构 | `DBX_PLUGIN_SIDECAR=<bin> python3 scripts/smoke_mcp.py`；容器场景无容器自动 SKIP |
| **L2 族级一致性** | schema↔行为漂移 + 同构契约 + 严格宿主形状 | `shared/mcp_schema_check.py`（A 静态 / **A+ 严格宿主透镜** / B 行为探针）；`shared/MCP_ACCEPTANCE.zh-CN.md` 契约表；`shared/test-all.sh` 四插件编排 | `python3 shared/mcp_schema_check.py --binary <bin> --plugin <id>`；`bash shared/test-all.sh [--quick]` |
| **L3 ZCode 真机接入** | 真实宿主（严格 zod 校验 + 真实进程管理 + 真实工具身份）下的端到端 | ZCode 用户级配置三服务器；§5 可重复清单 | 人工执行 §5（约 15 分钟） |
| **L4 深水区** | 多客户端并发、宿主超时、真实 DBX 桥（非 mock）、长稳 | 暂无自动化（§4 G7/G8） | 按需 |

## 2. 现状基线（2026-09-14 实测）

- L0：三插件全绿（files 222；ldap/kafka mcp 包全绿，含 required:null 红线）。
- L1：files 24 PASS/0 FAIL/7 SKIP（R1-R7 远端协议 env 门控）；ldap 19/19；
  kafka 18/18。
- L2：`mcp_schema_check.py` 三插件 CLEAN（含 A+ 严格透镜）；`test-all.sh`
  含 kafka 段（bash -n 通过，未在本机全量跑——dev-cluster 端口占用会按
  SKIP 落账）。
- L3：三服务器在 ZCode 会话内以 `mcp__dbx-*__*` 工具身份连通，三插件真实
  任务全链路通过（files：digest/write/cursor/两阶段删/UNAVAILABLE；ldap：
  digest/add/翻页/两阶段删/错误密码；kafka：digest/produce/投影聚合/两阶段
  topics_delete/只读门提示/未知 topic 提示）。
- 已实证红线：stdio 模式 stderr **无凭据泄漏**（zcode 会把 stderr 收进
  CLI jsonl 日志，此面必须保持干净）。

## 3. 「严格宿主」教训（为什么必须有 A+ 透镜）

2026-09-14 事故链：ldap/kafka 的 Go `toolEntry` 把 nil `required` 切片序列化
成 `"required":null` → ZCode 对 `tools/list` 逐字段 zod 校验，一项不合
**拒收整个服务器**（工具全部缺席）→ 而当时所有离线设施全绿：`schema_check`
的 `static_check_tool` 把非 list 的 required 静默折算为空列表容忍掉了，
smoke 的断言也只覆盖自家工具的 required 字段。

结论（已固化）：
1. **形状类校验不做类型折算**——`schema_check` A+ 透镜按严格宿主口径核对
   原始信封（required 缺席或字符串数组、name/description/inputSchema.type/
   properties 形状），违规即 DRIFT；
2. **sidecar 端红线测试**：序列化层 Go nil 切片问题由
   `tools_schema_test.go` 钉死（marshal 全文不得含 `"required":null`）；
3. **新 MCP 宿主接入前**必跑 L2 A+ 透镜（§6）。

## 4. 缺口登记

| # | 缺口 | 风险 | 处置 | 状态 |
| --- | --- | --- | --- | --- |
| G1 | 严格宿主形状校验未自动化（required:null 类问题只有真机才暴露） | 任一插件 schema 形状回退 → ZCode 整服务器拒收，且离线全绿无告警 | `mcp_schema_check.py` 新增 A+ 严格透镜（正/负向用例均验证）；纳入 §6 回归 | ✅ 2026-09-14 |
| G2 | `test-all.sh` 缺 kafka 段（族级一键验证漏掉第四插件） | kafka 回归依赖人肉记忆 | 补齐 kafka 段（go vet/test → 前端三件套 → compose 容器 smoke → MCP smoke），头注释改四插件 | ✅ 2026-09-14 |
| G3 | ZCode 真机验证为一次性手工动作，无可重复清单 | 后续改动无法低成本复验接入面 | 本文档 §5 固化清单；skill `dbx-mcp-usage` 引用 | ✅ 2026-09-14 |
| G4 | `MCP_ACCEPTANCE` §2 契约表过时（-32000「四插件一致」已因 stdio isError 分流失效） | 后续插件按旧契约「对齐」回错误形状 | §2 已改双形状表述 + 严格宿主红线小节 | ✅ 2026-09-14 |
| G5 | stderr 凭据泄漏检查仅手工实证（ldap happy path），未自动化、未覆盖错误路径与全部插件 | 凭据进宿主日志面（zcode 收集 stderr 入 jsonl） | 待办：smoke 增加场景——捕获 stderr 全文，断言不含本次内联密码/secret（含错误路径）；files/ssh Rust 侧同查 | ⬜ 排期 |
| G6 | 共享容器数据隔离不彻底（M15 已修 run 唯一前缀；其余场景仍假设基线数据，并行会话曾实测撞车 14≠12） | 并行开发/多会话共享测试容器时误报 | 约定：smoke 造数一律 run 唯一前缀 + 用后自清理；渐进改造到全部容器场景 | ⬜ 渐进 |
| G7 | 深水区未覆盖：多客户端并发 stdio、宿主 timeoutMs 行为、对**真实运行中 DBX 应用**的桥转发（现仅 mock 桥）、长稳/大树压测 | 低频路径回归无防护 | 已知未覆盖登记；出现相关缺陷时先补 L1 场景再修 | ⬜ 登记 |
| G8 | 二进制重建后的接入回归流程未强制（ZCode MCP 进程不热加载重建产物） | 「改了代码但行为没变」类困惑；旧进程持旧代码 | §5 清单第 0 步固化：重建 → L2 A+ → L1 → 重启会话 | ✅ 并入 §5 |
| G9 | ssh（Rust，已上线）未走本轮 ZCode 接入体检（`dbx-ssh` 已在 ZCode 连通，说明形状过关，但未按 §5 全清单复验） | ssh 真实任务面未系统验证 | 待办：按 §5 对 ssh 走一遍最小真实任务清单 | ⬜ 排期 |

## 5. ZCode 真机接入验证清单（可重复）

**第 0 步 · 接入前置（每次 sidecar 代码改动后）**

1. 重建二进制（files `cargo build --release`；ldap/kafka `go build -o bin/... .`）。
2. `python3 shared/mcp_schema_check.py --binary <bin> --plugin <id>` → 必须 CLEAN。
3. `DBX_PLUGIN_SIDECAR=<bin> python3 scripts/smoke_mcp.py` → 无 FAIL（SKIP 允许）。
4. 记住：**ZCode 的 MCP 进程不会热加载新二进制**——需重启会话或设置 → MCP 重连。

**第 1 步 · 接入配置**

- `~/.zcode/cli/config.json` → `mcp.servers` 加条目（`--mcp` + 专属
  `DBX_PLUGIN_DATA_DIR`，形状照既有 `dbx-ssh` 条目；schema 严格，勿加未知键）。
- 重启会话 → 确认 `mcp__dbx-<域>__*` 工具出现。缺席时查
  `~/.zcode/cli/log/zcode-*.jsonl` 搜 `mcp.server.failed`（G1 类问题在此现形）。

**第 2 步 · 每插件最小真实任务（tools/call 实跑，非 mock）**

- files：`files_scan_digest`（内联 `{"protocol":"local","root":…}`）→
  `files_write` → rows+`files_cursor_next` 翻页到 done → 两阶段 `files_delete`
  → `files_ui_focus` 应返回 UNAVAILABLE。
- ldap：内联凭据 `ldap_search_digest` → `ldap_entry_write` add → rows+cursor →
  两阶段 delete → 错误密码应报 Invalid Credentials 且不回显凭据。
- kafka：内联 brokers `kafka_messages_digest` → `kafka_messages_produce`
  （`readOnly:false`）→ `fields` 投影回读 → 两阶段 `kafka_topics_delete`
  （`allowDelete:true`）→ 不带 `readOnly:false` 的写应报可行动拒绝 →
  未知 topic 应报 stdio 注记。

**第 3 步 · 通用红线抽查**

- 错误路径：每次失败信息必须可行动（照着做能解除），不得原样重试即修复。
- stderr 抽查：任务期间 `~/.zcode/cli/log/zcode-*.jsonl` 不应出现内联凭据
  （自动化前的人工抽查，对应 G5）。
- 审计：写操作在 `<DBX_PLUGIN_DATA_DIR>/audit.jsonl` 落 `source:"mcp"`。

**第 4 步 · 收尾**：测试数据/scratch 资源自清理；结果记对应插件 PROGRESS。

## 6. 回归触发矩阵（何时必须跑哪层）

| 触发 | 必跑 |
| --- | --- |
| sidecar MCP 面代码改动（schema/错误形状/工具清单/协议层） | L0 相关包 → L1 该插件 smoke_mcp → L2 schema_check |
| sidecar 任意改动（含与 MCP 无关） | L0 → L1（test.sh 全套）；交付前 `test-all.sh` |
| 宿主同步 / DBX.app 更新（契约可能漂移） | L2 schema_check 四插件 → §5 第 1-2 步抽查受影响流 |
| 新 MCP 宿主接入（第二客户端、IDE 等） | L2 A+ 透镜 → §5 全清单（宿主校验口径可能不同） |
| 共享测试容器被多会话使用 | L1 全量重跑（防数据撞车，对应 G6） |

## 7. 与既有文档的关系

- `IMPL_PLAN_PLUGIN_MCP.zh-CN.md`：MCP 面设计来源（§1-§6 分层与两阶段语义）。
- `MCP_ACCEPTANCE.zh-CN.md`：四插件同构契约表（本文档 §3 的红线已回写其 §2）。
- 各插件 `docs/MCP.zh-CN.md`：协议权威文档（工具表、内联参数、语义差异）。
- skill `dbx-mcp-usage`：agent 调用视角的使用指引与常见坑（引用本文档）。
