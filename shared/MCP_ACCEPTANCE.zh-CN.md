# 四插件 MCP 验收用例清单（防形状漂移）

状态：2026-09-13 三轮 MCP 测试覆盖专项后沉淀（第一轮容错/准确性、第二轮深水区
实测、第三轮桥接兜底与错误码统一）。设计依据 `IMPL_PLAN_PLUGIN_MCP.zh-CN.md`
§3/§4/§5；各插件落地细节见各自 `docs/MCP.zh-CN.md` 与 `PROGRESS-P-*.zh-CN.md`。
**本文件是四插件 MCP 工具面的同构契约表**：改任何一插件触及下列条目时，
必须对照本表检查其余三家，漂移要么拉齐、要么在本表登记为已知差异。

## 1. 骨架与寻址

| 条目 | 契约 | 验收方式 |
| --- | --- | --- |
| stdio 模式 | `--mcp` 启动 JSON-RPC 2.0（MCP 2024-11-05），`initialize`→`tools/list`→`tools/call` | 各 smoke initialize/tools 段 |
| serverInfo.name | 完整插件 id（`io.dbx.ssh` / `io.dbx.ldap` / `io.dbx.files` / `io.dbx.kafka`） | smoke initialize 断言 |
| 方法面 | `mcp/tools`、`mcp/call`、`mcp/settings/get\|set`（workbench 桥模式）；stdio 为 JSON-RPC 面 | 各 smoke |
| 未注册方法/工具 | smoke 必须 **SKIP 不 FAIL**（硬性规则 5） | 各 smoke SKIP 语义 |

## 2. JSON-RPC 错误码分档（第三轮统一；第六轮按模式分流）

| 码 | 语义 | 说明 |
| --- | --- | --- |
| `-32700` | parse error | 四插件一致 |
| `-32600` | invalid request（无 id 等） | 四插件一致 |
| `-32601` | 未知 method（**非空**且非 mcp/* 的 method） | 第三轮由 ssh/files 从 -32000 统一为 -32601 |
| `-32602` | stdio 层结构性参数错误（如 arguments 非对象） | ldap/kafka 既有；files 第三轮补齐 |
| `-32000` | 工具级/应用级错误（含未知工具名） | **仅桥/插件协议模式（`mcp/call`）**；四插件一致 |
| `isError:true` | 工具级错误（stdio `tools/call`） | **stdio 模式统一为 MCP isError content**（MCP 规约：调用方须带内可见失败以自我纠正）。ldap/kafka 既有；files 2026-09-14 对齐（原 -32000）。结构性错误（缺工具名/arguments 非对象）仍 -32602；ssh stdio 侧是否跟随待其轮次决策（已知差异） |

**信封校验（第五轮统一）**：`jsonrpc` ≠ "2.0"、id 为 object/bool、缺或非字符串
method → 一律 `-32600`（四插件一致）；notification（无 id）静默零响应，连接继续可用。

**严格宿主形状红线（第六轮，2026-09-14 真机事故沉淀）**：zcode 等严格校验宿主对
`tools/list` 逐字段 zod 校验，**一项不合即拒收整个服务器**（症状：zcode 里该
MCP 服务器全部工具缺席，CLI 日志 `Invalid result for tools/list`）。已钉红线：
`required` 缺席或字符串数组——Go nil 切片序列化的 `"required":null` 必被拒
（ldap/kafka `toolEntry` 已修 + `tools_schema_test.go` 红线测试）。自动化门：
`shared/mcp_schema_check.py` 严格宿主透镜（A+ 段）。

smoke SKIP 判定按错误**文案**（"Method not found" / "Unknown tool"）与码解耦，
改码不破坏 SKIP 语义。

## 3. 参数容错语义（agent 输入变体）

1. **数字**：接受 JSON number / 整型浮点 / 数字字符串；存在但非法 → 报错**带原值点名参数**
   （`{key} must be an integer`），禁止静默回缺省或静默丢弃（历史教训：kafka
   `timestampMs:"…"` 曾折算为 0 等价重置到纪元；ssh `port:"2222"` 曾静默拨 22）。
2. **布尔**：接受 `true/false/1/0/yes/no/on/off`（大小写敏感保持原实现）；不可解析回退默认
   （files）或明确报错（kafka），禁止静默反转语义（历史教训：`readOnly:"true"` 曾解析为可写）。
3. **枚举**：大小写归一 + 常见别名（如 ldap scope `baseObject/singleLevel/subtree`）；
   未知值报错列合法值；「未知值按缺省降级」必须在文档标注为有意设计。
4. **可选字符串参数**：存在但类型错 → fail-fast 点名参数，禁止静默按缺省执行
   （历史教训：files `path:123` 曾静默扫整棵树）。
5. **未知参数**：静默忽略（严格 schema 校验归 MCP 宿主职责，sidecar 不重复建模），
   但未知参数不得影响已有参数解析。
6. **字符串列表**：接受数组（number 元素转字符串）与逗号串；空列表仍明确报错。
7. **校验顺序（第五轮登记为有意设计）**：连接参数门（含桥解析）先于业务参数
   校验——四插件现状一致，错误本身可操作（带内联参数指引）；类型/枚举等廉价
   校验在信封与分发层（-32600/-32602）已完成，业务参数的逐一校验在连接门之后。
8. **stdio 传输健壮性（第五轮族内契约）**：
   - 非法 JSON / 非法 UTF-8 字节流 → `-32700` + 续读下一行，进程存活（禁 `lines()`
     直接冒泡 InvalidData）；
   - 单行上限 16 MiB，超限整行丢弃回 `-32700`（消息带上限与 env 名）：
     `DBX_SSH_MCP_STDIO_MAX_LINE` / `DBX_FILES_MCP_STDIO_MAX_LINE`，ldap/kafka
     为常量 16 MiB；
   - pipelining：不等待响应连发多请求 → 响应与请求 id 一一对应；
   - 空行 / CRLF 容忍；每种敌意输入后合法请求必须仍成功。
9. **缺参报错口径（第七轮族内拉齐）**：四插件缺参报错均为**枚举式**——一次报出
   全部缺失的业务 required 参数（`Missing required parameters: a, b`，按 schema
   required 顺序；null 视同缺失；present-but-类型错误单独精确点名不混入）。
   files 的 `connectionId` 缺失保持单数 + 内联指引（对接 §3.7 连接门先行与核对器
   B1 口径）。

## 4. digest / cursor（读路径）

| 条目 | 契约值 |
| --- | --- |
| 默认 format | `digest`：`matched / scanned / stats / sample(≤5) / cursorId` |
| rows 上限 | 显式 `format:"rows"` 才出行，clamp ≤20/次 |
| 单元格截断 | 120 字符；定位字段（DN/path/partition-offset）不截断 |
| 单响应上限 | 16 KiB，超出置 `truncated:true` |
| cursor 物化 | 上限 1 万条；TTL 缺省 10 分钟（settings `cursorTtlSecs` 可调，各插件 clamp 范围见各自文档）；LRU ≤8 会话（`maxCursorSessions` 可调） |
| 空形状 | 空聚合输出 `{}`（禁 `omitempty` 吞键）；空批 `rows` 序列化为 `[]` 非 `null` |
| 过期报文 | 携带**实际生效 TTL** + 「重发 `*_search_digest`」指引 |
| 目标不存在 | 读侧兜底校验：kafka digest matched=0 时 DescribeTopic 存在性检查（不存在报错 + `kafka_ui_topics` 指引）；files 根路径拒绝；ldap 依赖服务端 filter 语义 |
| 投影可见性 | schema 解码失败/投影零命中必须可见（kafka `decodeFailures`/`fieldsNote`）；ldap `distinctAttr` 自动并入服务端投影 |
| **写入口径加固（第五轮）** | ldap：DN 控制字符（<0x20/0x7f）在 `ldapconn/normalizeLDAPWriteDN` 单点拒绝（RFC 4514 转义不受影响）；files：MCP 面路径形状门（`..`/`.` 段、控制字符、>4096 字节拒绝；`~` 不展开、`%2e%2e` 不解码为设计内行为，与 ssh 敏感路径门口径一致） |

## 5. 两阶段确认（写路径）

- 无 `confirmToken` → `preview` + 一次性 token（`c-<hex>`）+ `expiresAt`，不执行任何写。
- hash 绑定 = 归一化参数 canonical JSON **含 connectionId**（防跨连接复用）。
- 参数被改 → `hash_mismatch` 作废重开预览；token 一次性；TTL 缺省 60s
  （settings `confirmTtlSecs` 可调 10–600，已签发 token 不追溯）。
- 过期报文携带实际 TTL + "request a new preview"；消费失败也作废 token（须重开预览）。
- 只读连接/协议：写工具**不进 `mcp/tools` 清单**（注册即拒也要有第二道执行期门）。
- 预检前置：参数配套校验（如 kafka reset 模式）在签发 token **之前**完成，禁止白烧一次性令牌。
- **存储有界（第五轮）**：confirm 表签发时 prune 过期项（files 另有 1024 硬上限
  淘汰最早过期者）；长期 churn（500+ 轮）后表容量必须收敛。
- **cursor 真正 LRU（第五轮）**：命中即 touch，活跃会话不被纯插入序误逐
  （ldap/kafka 第五轮修复；files/ssh 既有）。

## 6. UI intent

- 状态机 `pending / applied / rejected / expired`；TTL 60s、LRU 20。
- `unknown intentId` 报错三要素：过期时长（60s/进程内）+ 「重发 `*_ui_*`」+ 「省略 intentId 读最新快照」。
- 超时返回 `pending` + 引导走 digest（降级矩阵 §5）。

## 7. stdio 桥接兜底（L1，第三轮四插件齐平）

- 端口发现 `mcp-bridge-port`（`DBX_APP_DATA_DIR` 覆盖，macOS 缺省路径）→ TCP 探测防陈旧端口
  → `DBX_APP_LAUNCH_CMD` 尽力拉起 + 500ms 轮询 ensure（预算 30s）。
- 契约：`POST /call-plugin-tool` snake_case 五字段（`plugin_id` = 本插件 id），64 KiB 单写上限，
  200 envelope 逐字透传，非 envelope 防御性包装。
- 转发范围：仅"需要连接"且显式未池化 `connectionId`；会话类工具（`*_cursor_next`）、UI 类、
  内联凭据在场 → 本地路径，不触桥、不污染本地池。
- fail-closed：失败统一 `DBX app bridge` 前缀 + 本插件字段族的内联凭据出路，不假死。
- 已知差异（登记不阻塞）：ssh 桥含 `/list-plugin-connections` 双源合并；ldap/files/kafka
  无连接名寻址需求，只移植 ensure+call。

## 8. settings 可调键族

files/ldap/kafka 同构：`digestScanLimit`（kafka/ldap）、`maxCursorRows` / `cursorTtlSecs` /
`maxCursorSessions` / `confirmTtlSecs`；ssh 按自身工具面有 localTransferRoot /
maxUploadBytes / execPermissionMode / connectionScope 等同款模式。语义统一：
白名单部分更新 + 非法值明确拒绝 + clamp 即时生效、已存在会话/令牌不追溯。

## 9. 验收基线（2026-09-14 七轮后）

| 插件 | 单测 | smoke（MCP） | 说明 |
| --- | --- | --- | --- |
| ssh | cargo test **398** | 离线+在线 all green（31 工具 + 协议健壮性/行上限/缺参枚举/enum 在线/在线 pipelining 段） | `-race` 不适用（Rust），并发面为逻辑竞态测试 + 在线 pipelining |
| ldap | mcp 包 **97 函数** | **20/20 PASS**（OpenLDAP 容器） | `go test -race` 全绿零告警；含并发压力×3、M20 enum 在线 |
| files | cargo test **229** | **31 场景**（无容器 24 PASS 7 SKIP；容器下 26 PASS） | 并发压力×3；R1/R4 含 enum 在线断言 |
| kafka | mcp 包 **94 函数**（kafkaconn 184） | **19/19 PASS**（dev 集群） | `go test -race` 全绿零告警；含并发压力×3、K19 enum 在线 |

**桥回环（第七轮 --deep，第八轮终验）**：四插件「转发 → 宿主 → app 内插件 sidecar
→ 真实端点 → 结果回流」全链路打通（ssh vagrant 真执行 + ldap/kafka/files seed
连接真实 digest，`shared/host-e2e/mcp_bridge_e2e.sh --deep` 幂等复跑）。
**第八轮以第七轮后重打包的最新 dbxp 复跑：7/7 PASS**（ssh 0.4.67 / ldap
0.1.64 / files 0.1.55 / kafka 0.1.35）；ldap secret 路径（bind_password 经
connection_secrets 回填）以三态对照真机实证成立（正确密码真实回流 / 错误
密码 code 49 / 禁匿名容器匿名 code 48）。

**全容器终验（第八轮）**：files 七协议 framed 186 PASS + MCP 30 PASS/1 SKIP
（R7 oss 按设计 SKIP，R2/R3/R5/R6 enum 在线断言自然带跑通过）；ldap 20/20；
kafka 19/19；ssh 离线 all green——第七轮全部改动在真实容器/集群组合下无
回归。性能基线（MCP 专项后首个）已采集：无数量级回退（ssh 下载反高于历史
30%+；上传段受并行构建负载影响偏保守，建议空载复采校准）。

## 10. 已知残余风险（跟进项，第八轮终验后）

- **宿主 runInTerminal 冷启动竞态**（ssh e2e T1/T5 复现，宿主侧待修，需按
  AGENTS.md 规则 8 立项；桥链路本身不受影响，隐藏通道全绿）。
- **R7 oss** 依赖真实 OSS endpoint（env-gated，设计如此）。
- **性能基线**在并行构建负载下采集，数值偏保守，建议空载复采校准。
- 宿主侧若对 seed 连接触发全量 `save_connections` 会丢运行中 seed 行
  （e2e 流程无此操作；`mcp_bridge_e2e.sh --deep` 幂等自愈）。
- 真机 DBX.app 桥端到端契约由 mock + 隔离真机双层钉死，宿主异动按
  AGENTS.md 规则 7 走 `scripts/host-sync.sh contract` 单点跟进。
- kafka `topicNotFoundish` 等上游错误文本宽松匹配，franz-go 大版本升级需复核。
- stdio 唤醒预算（ensure 30s）即被测行为，各 smoke 桥段有对应确定性耗时。

## 11. schema↔行为一致性核对（2026-09-13，第五轮）

核对器 `shared/mcp_schema_check.py`（python3 标准库，只读探针，不落插件目录）。
用法：`python3 shared/mcp_schema_check.py --binary <sidecar> [--plugin io.dbx.xxx]`
（`--probe-timeout` 默认 5s；超时视为意外触发连接级执行，记 probe-not-applicable
并重启 sidecar）。流程：spawn `--mcp` → initialize → tools/list → 逐工具
A) schema 静态校验（type 合法 / required ⊆ properties / enum 非空互异 /
工具与参数级 description 非空 / 工具名 `<域>_<动作>` / 参数名 camelCase /
草案外关键字提示）；B) 行为探针（空参缺参点名比对 required、required 参数逐个
类型错值、required enum 非法值、未知参数只验不崩）。

判定口径（本表 §3 全部豁免条款已内置）：
- **DRIFT**（退出码 1）：schema 与行为矛盾——静默接受类型错值、缺参错误漏点名/
  多报/名字不一致、enum 错误未列合法值。
- **WARN**：登记设计或探针不适用——统一门禁消息先于参数校验（工作台门/连接门，
  含「连接参数门先于业务参数校验」的 §3.7 顺序张力）、布尔不可解析回退默认
  （§3.2 files）、枚举非法值静默降级（§3.3 须文档标注）。
- **HINT**：草案外关键字等提示级，不影响退出码。
- 数字字符串对 number 字段合法（§3.1）、未知参数静默忽略（§3.5）、枚举大小写
  归一（§3.3）均按设计豁免，本轮零误报。

结果（快照时点：ssh 09:31、ldap/kafka 23:17、files 00:12+，files 在核对期间
被并行构建更新过一次，行为以末次运行为准）：

| 插件 | 工具 | PASS | DRIFT | WARN | 结论 |
| --- | --- | --- | --- | --- | --- |
| ssh | 31 | 6 | 121 | 28 | **DRIFT** |
| files | 12 | 12 | 0 | 18 | CLEAN |
| ldap | 8 | 8 | 0 | 11 | CLEAN |
| kafka | 11 | 11 | 0 | 17 | CLEAN |

ssh 漂移清单（修复轮输入）：
1. **参数级 description 缺失 118 项**（24/31 工具）：共享参数族
   `authFlowMode` / `authentication` / `connectTimeoutSecs` / `passwordPromptHint` /
   `totpPromptHint` 在多数工具 schema 中无 description（其余参数齐全，
   全插件 118/457 参数槽位缺失）。LLM 依赖 schema 调参，属直接易用性缺口。
2. **缺参错误首参 fail-fast 只报首个缺失参数 3 项**：`ssh_multi_exec`（只报
   `command` 漏 `targets`）、`sftp_upload` / `sftp_download`（只报 `localPath`
   漏 `remotePath`）。错误质量尚可但与 required 全集不一致，agent 需多轮补参。

跨插件共性（WARN 级，登记不阻塞，修复轮可决策）：
- **连接参数门先于业务参数校验**：files 写路径五工具（write/mkdir/rename/
  delete/purge，非法 connectionId 触发桥解析先于 path 类型校验）、ldap
  `search_digest`/`entry_write`、kafka 全部连接级工具（空参先报
  "connection parameters required" 而非业务缺参）。与 §3.7「类型/枚举先于
  连接级校验」字面顺序存在张力；现状错误本身可操作（含 inline 参数指引），
  建议要么把廉价参数校验提到连接解析前、要么在本表把该顺序登记为有意设计。
- **缺参逐参数 fail-fast** 为四插件共同风格（每轮只点名一个缺失参数），
  与 ssh 的 3 项 DRIFT 同根源，拉齐口径时可一并决策。
- UI 工具（`*_ui_*`）在独立 stdio 模式被工作台门整体拦截（UNAVAILABLE），
  参数校验不可达——设计行为，探针不适用。
- 静态面四插件全绿：工具名/参数名命名、required ⊆ properties、enum 形状、
  草案内关键字、工具级 description 均无漂移。
- 覆盖边界：enum 非法值报错列合法值（§3.3）在多数工具上被连接门/工作台门
  拦截无法离线探到，已探到的路径（如 cursor/本地工具）无异常；该条需在线
  冒烟继续覆盖。

**终验（第六轮后，2026-09-13）**：ssh 完成修复轮（description 常量单点化回填
118 处、缺参枚举、16 MiB 行上限）后，四插件在**新鲜二进制**上复跑核对器全部
`RESULT: CLEAN`（ssh 31/31 PASS、DRIFT 121→0；files 12/12、ldap 8/8、kafka
11/11 维持 CLEAN）。ssh 缺参枚举式与其余单参数 fail-fast 的口径差异登记于 §3.9。
