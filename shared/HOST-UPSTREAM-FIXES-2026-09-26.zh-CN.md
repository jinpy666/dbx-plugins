# Host 上游修复批次（2026-09-26）

依据：`shared/REVIEW-FAMILY-2026-09-26.zh-CN.md` review 结论（host main @
`a71e4534a` = 纯 upstream/main，fork 零独有提交）。本轮按"一个分支 = 一个
上游 PR"组织，全部**非破坏性变更**，每个分支独立可合、互不依赖。

## 提交纪律（AGENTS.md 规则 8 说明）

- 为什么必须改 host：五项问题全部位于宿主内部（MCP 桥鉴权与绑定、安装器
  持久化、manifest schema、feature 矩阵），`shared/frontend/` 公共适配层与
  插件侧均无法触及，只能上游修复。
- 改动隔离：host/ 内每个 PR 一个独立分支（基于 main，不含任何本地长期补丁），
  main 本身保持与 upstream/main 一致；本轮未推送任何远端、未创建 PR。
- 上游冲突风险：低。各分支改动均局部（mcp_bridge.rs / installer+marketplace /
  schema+manifest 测试 / dbx-drivers Cargo.toml），与上游活跃改动区重叠少。

## A 类：MCP 桥安全（最高优先级）

### PR-1 `fix/bridge-plugin-id-binding` @ a46f262e5

- **问题**：`/call-plugin-tool` 用请求里的 `plugin_id` 决定路由哪个插件的
  sidecar，而 lifecycle（含连接明文凭据）始终由连接配置构造。请求可指向
  插件 B，让 B 拿到插件 A 连接的凭据。
- **原因**：`req.plugin_id.or_else(|| config.plugin_id.clone())` 无一致性
  校验（mcp_bridge.rs 原 1384 行）。
- **解决**：抽 `resolve_call_plugin_target()`——只能调用连接自身的插件；
  请求可回显绑定值，不可覆盖；不一致或连接未绑定插件一律 400。真实调用方
  （ssh/files sidecar 的 app_bridge）传的就是自身 id，不受影响。
- **复现**：对 SSH 连接 POST `/call-plugin-tool` 且 `plugin_id="io.dbx.files"`
  ——修复前 files sidecar 收到 SSH 连接 lifecycle；修复后 400。
- **验证**：`cargo test -p dbx --lib mcp_bridge` 19 passed（含 2 个新测试）。

### PR-2 `fix/bridge-transport-hardening` @ d5f296104

- **问题**：① 单次 64KiB 读截断大请求体，截断落在 JSON 边界时会把被砍的
  SQL 送执行；② 无任何来源校验，恶意网页可对回环端口发免预检跨站 POST
  （fire-and-forget CSRF）；③ 端口文件默认 0644，同机其他用户可读。
- **解决**：按头部块 + `Content-Length` 循环读（上限 10MiB）；拒绝带
  `Origin` 的请求（浏览器跨站必带）、非 `application/json` 的 POST（浏览器
  免预检 POST 发不出）、`Host` 非回环形式（DNS rebinding）；端口文件 0600
  （含已存在文件的权限修正）。合法本地客户端（sidecar、dbx-mcp、dbx-cli）
  均带 Host + JSON 头、无 Origin，不受影响。
- **复现**：`nc` 分两次写入 >64KiB 的 `/execute-query` 请求——修复前 SQL
  尾部被丢弃；修复后完整执行。网页 `fetch()` 跨站调用现在得到 403。
- **验证**：20 passed（新增 Content-Length 解析、100KB 分段读取、0600 权限、
  本地客户端判定矩阵四组测试）。

### PR-3（后续轮次，未实施）：桥 token 鉴权

- **为什么拆出去**：强制 token 要求所有读端口文件的调用方同步升级（ssh/
  files sidecar、dbx-mcp、dbx-cli、shared/host-e2e），属跨仓协调变更，做在
  同一轮会破坏"非破坏性"约束。PR-2 已挡住浏览器类威胁（现实威胁面），
  token 只针对"本机恶意进程读取端口文件"这一更低概率路径。
- **方案**：新增 `mcp-bridge-token` 文件（0600，随机 hex）；端口文件保持
  纯文本端口号不变（兼容全部现存读者）；宿主校验 `X-DBX-Bridge-Token`；
  ssh/files sidecar 增加"token 文件存在才带头"的兼容逻辑。
- **前置**：先在本轮 PR-1/2 合入后，给 ssh、files 插件的 app_bridge 发
  兼容性更新（我们的仓），再向宿主发 token PR。

## B 类：安装器/市场数据完整性

### PR-4 `fix/plugin-installer-atomic-writes` @ 3f2f0a8e1

- **问题 1**：`write_json_atomically`（installer.rs 与 marketplace.rs 各一份）
  先 remove 再 rename，中间崩溃会丢掉信任库 keys.json / 仓库文档——与
  "atomically" 的名字直接矛盾。
- **解决 1**：直接 `rename_with_transient_lock_retry` 原地覆盖（POSIX 与
  Windows `MoveFileEx REPLACE_EXISTING` 语义均为原子替换），顺带获得对杀毒
  软件短暂持锁的重试保护。
- **问题 2**：`migrate_legacy_container` 先搬载荷再写 activation record，
  回滚路径会 `remove_dir_all(container_dir)`——record 写失败时把旧插件唯一
  副本连同回滚一起删掉。
- **解决 2**：调换顺序，先写 record 再搬载荷：任何步骤仍可能失败时容器里
  只有空脚手架，回滚不再可能丢数据。
- **复现 2**：向 `write_activation_record` 注入失败跑 legacy 迁移——修复前
  container_dir 被删；修复后回滚恢复原 flat 布局。
- **验证**：`cargo test -p dbx-plugin-runtime` 全量 149 passed；新增
  `atomic_json_writes_replace_in_place_without_temp_litter`，回归
  `installing_v1_migrates_legacy_flat_plugin_and_preserves_rollback` 通过
  （两个测试已在分支上点名复验）。

## C 类：契约面一致性

### PR-5 `fix/manifest-schema-command-menus` @ 5dca6e9e8

- **问题**：运行时 `PluginContribution` 有 7 个变体（含 command/menus），
  发布的 `manifest.schema.json` oneOf 只列 5 个——用 schema 校验的 CI/
  编辑器会拒绝宿主自身接受的合法 manifest。
- **解决**：补 `commandContribution`/`menusContribution`（及
  commandAction/openWorkbenchAction/menuItem/commandEnablement）入 oneOf；
  openWorkbenchAction 的可选字段**有意保持宽松**（运行时已有
  deny_unknown_fields 把关，schema 逐项复读会重演本次漂移）。新增 parity
  测试把 oneOf 清单与运行时 7 个 type tag 钉死，下次加变体不改 schema 就
  红 CI。
- **复现**：用旧 schema 校验带 command/menus 贡献的 manifest 即失败。
- **验证**：parity 测试在分支上点名通过；`python3 -m json.tool` 校验
  schema JSON 合法。

## D 类：构建矩阵

### PR-6 `fix/drivers-duckdb-sidecar-linkage` @ c589308e8

- **问题**：`e0da2d09d` crate 拆分把 `dbx-drivers/duckdb-sidecar` 变成空
  feature，不再联动 `dbx-sql-schema/duckdb-sidecar`（DuckDB DDL 门控所在），
  只开 drivers 侧 feature 会得到"有 worker 协议、无 DDL 门控"的半开组合，
  编译不报错、运行期缺行为（同类漂移 bfc834c77 已发生过一次）。
- **解决**：恢复联动 `duckdb-sidecar = ["dbx-sql-schema/duckdb-sidecar"]`
  并加依赖边（已验证无环：schema 只依赖 sql-core/sql-dialect/types），
  Cargo.lock 随附；foundation CI 组本就覆盖 schema 侧 feature，drivers 组
  经传递现在也编到该组合。
- **复现**：`cargo tree -p dbx-drivers --features duckdb-sidecar -e features`
  修复前无 schema feature 激活，修复后有。
- **验证**：`cargo metadata --locked --offline` 通过（lock 已同步）。

## E 类：本仓修复（随 main 直接提交）

### R0 `scripts/host-sync.sh` 契约监视失明修复

- **问题**：`CONTRACT_PATHS` 监视的 `crates/dbx-core/src/plugins/host.rs`
  在上游 `bc0043183`（9-18 crate 拆分）中已移至
  `crates/dbx-plugin-runtime/src/plugins/host.rs`，规则 7 的强制 `contract`
  动作对后端契约变更静默失明；且从未覆盖 `manifest.rs`（Host API 1.4 常量、
  features/permissions 表）与 Rust SDK。另外同步基准仍指向 fork 的
  `origin/main`（已落后 upstream 161 提交）与已退役的
  `dev/plugin-framework-current` 分支。
- **解决**：基准改为 `upstream/main`（首次自动补 upstream remote）；清单改
  为 5 个前端桥文件 + `crates/dbx-plugin-runtime/src/plugins/` 目录 +
  `plugins/sdk/rust/dbx-plugin-sdk/` 目录；`contract_watch` 开头对每个路径
  做存在性断言，路径消失即 FAIL 并提示更新清单（防再次失明）。
- **验证**：`bash -n` 通过；`status` 输出 ahead 0 / behind 0；`contract`
  正常出快照；负例确认旧路径会触发 FAIL（exit 128→脚本退出）。
- **注意**：该改动在本仓工作区**未提交**，待确认后随 chore 提交。

## 后续轮次清单（本轮未实施，按优先级）

| # | 项 | 原因/方案摘要 | 前置 |
| --- | --- | --- | --- |
| 1 | 桥 token 鉴权（PR-3） | 见 A 类；token 文件 0600 + 头校验 | ssh/files sidecar 兼容更新 |
| 2 | 生产 CSP 收紧（tauri.conf.json:33 `csp: null`） | XSS 最后防线；需 dev/prod 两套 + CI 断言 | **真机全功能回归**（Tailwind inline style、插件 iframe、自定义协议均要验证），无法离线确认，不冒进 |
| 3 | v-html 统一 sanitize 收口 | 30+ 处分散手写转义，3 套 marked renderer 合并 + DOMPurify 兜底 | 前端基建决策（引入依赖），建议先开上游讨论 |
| 4 | 连接 deep-link 禁 http 投递 .dbxp / 弱化 password 参数 | `pluginInstallDeepLink.ts:28`、`connectionDeepLink.ts:70`；涉及本地开发流，需上游确认策略 | 上游 issue 讨论 |
| 5 | mysql 裸 SHOW TABLES 回退加 `SELECT DATABASE()` 守卫 | `dbx-driver-mysql/src/mysql.rs:3609`，防未来调用方传错 conn 静默返回错库表 | driver 侧行为改动，单独 PR + 测试 |
| 6 | Redis 集群删除按槽位批 512 + `deletedCount` 去重 | `dbx-core/src/query/redis_ops.rs:1111`、`RedisKeyBrowser.vue:1800` | 性能/正确性各一 PR |
| 7 | 宿主握手契约一致性测试（architect "只改一件事"） | 用真实 SDK crate 跑 `plugin/initialize`，断言 features 可被 `supports()` 命中；同时治 dot/slash 命名分裂 | 上游接受测试依赖方向后实施 |
| 8 | dbx-core 继续拆分（transfer.rs 1.7 万行等） | 上游自身演进节奏，跟随即可 | — |

## 上游 PR 提交建议

1. 推送顺序即优先级：PR-1 → PR-2 → PR-4 → PR-5 → PR-6。
2. 五个分支互不依赖、基于 upstream/main `a71e4534a`，可分别推送：
   `git -C host push <fork或上游> fix/<branch>` 后开 PR（推 upstream 仓库
   分支或 fork 均可，按 t8y2/dbx 的贡献流程）。
3. PR 描述可直接取各 commit message 的 Motivation/Fix/Repro 段落。
4. 全部 Rust 测试为本地实跑结果；src-tauri 侧未跑完整 `cargo test -p dbx`
   （仅 mcp_bridge 过滤集），CI 兜底。
