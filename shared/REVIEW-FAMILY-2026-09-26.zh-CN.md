# DBX 插件族四插件综合审查报告（2026-09-26）

> 审查方式：5 个并发 agent 只读深度审查（ssh / ldap / files / kafka 各 1 + 跨插件 UI 一致性横切 1），
> 关键 HIGH 级发现已由主会话逐条抽查验证（grep/源码比对）。全程未修改任何文件。
> 严重级别：CRITICAL（未发现）/ HIGH（应尽快修）/ MEDIUM（计划内修）/ LOW（择机）。

## 一、审查范围与规模

| 对象 | 后端 | 前端 | 说明 |
| --- | --- | --- | --- |
| ssh（io.dbx.ssh） | Rust 35 文件 / 45,947 行 | Vue3 ≈29,815 行（App.vue 9,570 行） | v0.2.x 已上线 |
| ldap（io.dbx.ldap） | Go ≈16,900 行 | TS/Vue ≈30,000 行 | stdio-jsonl |
| files（io.dbx.files） | Rust 32 文件 / 37,936 行 | ≈20,200 行 | Rust+OpenDAL+rclone 编排 |
| kafka（io.dbx.kafka） | Go 15,132 行（311 测试） | 18,688 行 | Go+franz-go |
| shared/frontend + 四插件前端横切 | 5 模块（binaryEvent/themeSync/uiIntent/editorTheme/pluginStorage） | — | 公共适配层唯一实现点 |

忽略 `.worktrees/`、`node_modules/`、`frontend/dist/`、`target/`、`ui/`（构建产物）。

## 二、结论摘要（TL;DR）

- **CRITICAL：0**。四插件的核心安全红线（凭据 secret binding、两阶段写确认、read-only 门禁、
  主机密钥 TOFU、审计脱敏、七语 i18n、公共层零复制）经通读全部成立，且普遍有测试钉死——整体
  工程纪律显著高于同类工具。
- **HIGH：11 条**（ssh 2、ldap 2、files 1、kafka 1、跨插件 5），集中在三类：
  ① 沙箱内 `window.confirm` 残留导致功能静默失效（ssh）；
  ② 各自的"最后一个危险面"（ssh 灾难门漏 `xargs`、ldap 搜索聚合无硬上限、files serve 无鉴权、
  kafka 解压炸弹 OOM）；
  ③ 跨插件契约漂移：`pluginStorage.ts` 未入库、ssh/kafka 订阅宿主桥不存在的幽灵 API、
  appearance.ts 四份拷贝、mock 镜像幽灵 API（正是规则 7 要防的脱节）。
- **MEDIUM 23 条 / LOW 25 条**，主要是性能（无界读取、锁内传输、热路径重复编译）与一致性细节
  （语义色散装 hex、通知/确认/表格组件四种形态）。
- 最具杠杆的两项收敛：**一次 `shared/frontend/` 单点化**（hostBridge 类型 + env/context/locale/
  theme 运行时 + appearance），可同时根治 5 条跨插件 HIGH；**一次审计/上限补齐**，覆盖 files
  审计盲区与 ldap/kafka 内存上限。

## 三、HIGH 级问题全量清单（已逐条验证）

### ssh

| 编号 | 维度 | 位置 | 问题 | 解决方案 |
| --- | --- | --- | --- | --- |
| SSH-H1 | UI 正确性 | `ssh/frontend/src/App.vue:4486,4503,4625,4776,5145,5396,6130,6636` | 8 处残留 `window.confirm`；宿主沙箱 iframe 无 `allow-modals` 恒返回 false，重命名覆盖/粘贴覆盖/快速命令删除/进程强杀等在真机上**静默失效**（代码注释自己记录过根因，属修复未收敛的残留） | 统一替换为既有的应用内确认弹窗模式（复用录制删除同款 ref+Dialog）；CI 加 lint：`App.vue` 中 `window.confirm` 应为 0 |
| SSH-H2 | 安全 | `ssh/backend/src/mcp_safety.rs`（`xargs` 全文 0 命中，wrapper/灾难动词表 :439-471） | `echo x \| xargs rm -rf /` 分类 Unknown，可写连接上**无需 confirmDestructive 即执行**；脚本解释器（`python -c`/`perl -e` 等）同理。工具描述向调用方承诺"灾难命令需人工确认"，一条 shell 常识管道即可绕过（提示注入下的 LLM 可借此免确认毁损） | ① `xargs` 加入 wrapper 展开（尾段灾难动词则升级 Destructive）；② 脚本解释器 `perl -e`/`python -c`/`ruby -e`/`node -e`/`awk`+`system(` 返回 Destructive；③ 补对抗测试用例 |

### ldap

| 编号 | 维度 | 位置 | 问题 | 解决方案 |
| --- | --- | --- | --- | --- |
| LDAP-H1 | 安全 | `ldap/backend/internal/ldapconn/operations.go:652,669` | modifyDn 的 `newRDN`/`newSuperior` 只做 `TrimSpace`，漏掉 `normalizeLDAPWriteDN` 已有的裸控制字符防护（policy.go:65 为 go-ldap ParseDN 不报换行/空字节专门加固过主 DN）——注入风格 RDN/新父 DN 绕过本地三道校验直达服务端并进审计/展示面 | 对 newRDN/newSuperior 补 `hasRawControlChar`（收敛为导出的 normalizeLDAPWriteRDN/newSuperior），MCP 预检同步调用；补 policy_test 用例 |
| LDAP-H2 | 性能 | `operations.go:904-910` + `mcp/server.go:382-385`、`tools.go:90-103` | 搜索客户端聚合上限无防御硬上限：显式 sizeLimit 原样透传，条目（含 `*` 全属性值）全量物化进 sidecar 内存；且 MCP `ldap_search_digest` 接受 schema 未声明的 `sizeLimit` 参数，严格/宽松宿主行为不一致。对照组 count=5000、子树删除=1000 都有顶，唯独主搜索没有 | `aggregateLimit` 加防御上限（如 10 万，超出 clamp+truncated）；tools.go schema 补声明 sizeLimit 或删除隐藏读取 |

### files

| 编号 | 维度 | 位置 | 问题 | 解决方案 |
| --- | --- | --- | --- | --- |
| FILES-H1 | 安全 | `files/backend/src/main.rs:1655-1713`、`rclone/rc.rs:410-431` | `files/serve/start` 以**无鉴权** rcd serve 暴露远端子树（仅回环绑定）：同机任意进程可匿名下载；浏览器 DNS rebinding 可绕同源策略抽取 `127.0.0.1` 内容。自家 M1 网关（webdav_gateway.rs）正是为此用 256-bit token，serve 回退到了更弱形态 | 为每次 serve 生成随机 token 走 rcd `--baseurl`（URL=`/token/`，与网关同构），或传随机 `--user/--pass` |

### kafka

| 编号 | 维度 | 位置 | 问题 | 解决方案 |
| --- | --- | --- | --- | --- |
| KAFKA-H1 | 安全/性能 | `kafka/backend/internal/kafkaconn/messages.go:1427,1430,1452` + `:1470`、`mcp/server.go:501` | 解压炸弹：gzip/snappy/zstd `io.ReadAll` 无上限，单条恶意消息可把 sidecar 打到 OOM（MCP 工具 schema 直接暴露 `decompression` 参数可触发）；且 `valueText` 对解码后完整 value 不截断（base64 侧有 512KB 截断，双通道策略不一致），digest 最多 10 万条 × 双字段留存也是 GB 级驻留 | `decompressPayload` 全分支 `io.LimitReader(reader, maxDecodedBytes+1)` 超限报错；`valueText` 与 base64 同用 maxMessageBytes 截断并置 truncated（前端已有提示链路） |

### 跨插件（UI 体验一致性 / 契约）

| 编号 | 维度 | 位置 | 问题 | 解决方案 |
| --- | --- | --- | --- | --- |
| X-P1 | 工程风险 | `shared/frontend/pluginStorage.ts`（git 未跟踪，已验证 `git ls-files` 不含） | 四插件已全部 import 且 `main.ts` 挂载前 `await pluginStore.ready`，但文件未 `git add`——干净检出构建四插件**全红** | 立即 `git add` pluginStorage.ts 与 README 修改并提交 |
| X-P2 | 桥接降级 | `kafka/frontend/src/App.vue:407`、`ssh/frontend/src/App.vue:7610` | 订阅宿主桥**不存在**的 `onLocaleChange`（真桥 pluginHostBridge.ts 全文 0 命中，已验证）；locale 由 env 消息推送（真桥 :422-425）。宿主切语言后 ssh/kafka 工作台停留旧语言，ldap/files 正常跟随——"一半切了一半没切" | ssh/kafka 的 handleEvent 补 `event.type==="env"` → locale 分支（照抄 ldap App.vue:797-800）；中期收敛到 shared 单点（见 X-P4） |
| X-P3 | 桥接降级 | `kafka/frontend/src/App.vue:412`、`ssh/frontend/src/App.vue:7612` | 同病：订阅不存在的 `onContextChange`（真桥只有 `onContext`），context 重推静默失效，保持旧连接语义 | 短期补 `api.onContext ?? api.onContextChange` 兼容行（ldap 已有 :838-840）；随 X-P4 收敛 |
| X-P4 | 一致性 | 四插件 `frontend/src/lib/appearance.ts`（已验证 md5：kafka≡ldap，ssh 仅差终端字体行，files 精简版） | 宿主外观适配**四份拷贝**，且 applyAppearance 三种写法：仅 kafka 先探测宿主令牌再移除内联（正确）；ssh/ldap/files 无条件 setProperty 内联覆盖 themeSync 的 `var()` 引用——appearance 事件后停止跟随宿主主题 | `shared/frontend/` 新建 appearance 单点（kafka 的探测-移除策略为准），可并入 `hostThemeRuntime.ts` 与 P2/P3 一并收敛 |
| X-P5 | mock 契约（规则 7） | `kafka/frontend/src/mockDbxHost.ts:1185`、`ssh/frontend/src/mockDbxHost.ts:995`、`ldap/mockDbxHost.ts:640-645` | mock 宿主提供真桥没有的幽灵 API（onContextChange/onLocaleChange/onAppearanceChange），夹具全绿而真机静默失效——正是规则 7 明文要防的脱节，也是 P2/P3 长期无人发现的原因 | 以真桥 pluginHostBridge.ts API 面为准裁剪四份 mock，env 推送改经 onEvent/`dbx-plugin-env` 分发（与真桥 :1148/:1153 同构） |

## 四、MEDIUM 级问题（23 条，按插件）

### ssh（4 条）
| 编号 | 维度 | 位置 | 摘要 → 方案 |
| --- | --- | --- | --- |
| SSH-M1 | 安全/资源 | `sudo_fs.rs:396-443` + `main.rs:634-642` | `sudo/readFile` 缺省 length=0 全量读入内存（≈2.3×文件大小），对照 sftp/read 有 256KB 钳制 → 改用 `bounded_bytes(...,256*1024)` 并返回 truncated |
| SSH-M2 | 性能 | `session_recording.rs:228-243,265` | 录制回放/列表每次 `read_to_string` 整个 `.cast`（可达数百 MB）→ read_events 改 BufReader 流式；describe 只用 Seek 从文件尾回溯取末行 |
| SSH-M3 | 性能/锁 | `mcp.rs:2482-2504,2583-2632` | SFTP 上传/下载**全程持有连接池全局 write() 锁**，一次传输串行化所有连接的 MCP 调用（数十秒假死）→ 锁内只取 handle/sftp 的 Arc 克隆即 drop(guard)，传输在锁外 |
| SSH-M4 | 资源 | `mcp.rs:2450-2463` | `sftp_upload` 先 `fs::read` 整个本地文件再判 16MB 上限 → metadata 先判（对齐下载侧 stat-first） |

### ldap（2 条）
| 编号 | 维度 | 位置 | 摘要 → 方案 |
| --- | --- | --- | --- |
| LDAP-M1 | 契约 | `service.go:467-473` vs `frontend/src/lib/auditFeed.ts:22-24` | tls-insecure 审计 `result:"warning"` 越出 ok/denied/error 三值契约，前端折算成红色 error 误导排障 → 二选一全链对齐：改发 ok+Detail，或正式引入 warning 态 |
| LDAP-M2 | UI 一致性 | `style.css:117-118,286`、EntryEditorDialog.vue:1388、PasswordAttributeEditor.vue:301 | 语义色散装 hex `#10b981`/`#d97706`（正是 shared/frontend README 点名禁用的色值）→ 警示语义改 `var(--warning)`；装饰色集中注释豁免理由 |

### files（5 条）
| 编号 | 维度 | 位置 | 摘要 → 方案 |
| --- | --- | --- | --- |
| FILES-M1 | 安全 | `rclone/proc.rs:131-143` | rcd `--rc-user/--rc-pass` 走 argv，同机他用户 `ps` 可见并可 `config/dump` 该组全部连接凭据 → 注入 `RCLONE_RC_USER/RCLONE_RC_PASS` 环境变量（复用 RcdEnv 机制），argv 只留 addr/config |
| FILES-M2 | 性能/健壮 | `rclone/rc.rs:158-169`、`bytes_channel.rs:330-351`、`main.rs:4704-4733` | 传输路径零超时：rcd 卡死时 job 永久 running、取消无法中断挂起的 `chunk().await`、slot/WorkGuard 不释放 → chunk 读取处包 `tokio::time::timeout`（60-120s 读空闲超时），保留大文件无总上限语义 |
| FILES-M3 | 安全/正确 | `policy.rs:264-288`、`registry.rs:641-653` | Windows 反斜杠根路径被 sanitize 拒绝后 canonical_root **静默回落 `/`，lock_to_root 门禁形同虚设**（`C:\data` 与 `C:/data` 行为不一致且无告警）→ canonical_root 识别盘符先把 `\` 归一 `/`，或连接保存时显式报错；补回归测试 |
| FILES-M4 | 契约 | `mcp/definitions.rs:189-191` vs `tools.rs:600-605` | MCP files_rename 描述承诺"目录改名降级 jobId"，实现直走 movefile（目录 404），与工作台 dirJob 路径不一致 → 修描述为实际行为，或 MCP 路径补 dirJob 降级（starter 已存在） |
| FILES-M5 | 审计完整性 | `main.rs:459-523,552-631,2421-2530,4500-4577` | files/write 内联写、mkdir/rmdir、upload finish、remote-edit 回传**均不写 audit.jsonl**（copy/move/delete/purge 有），审计权威轨迹有盲区 → 各补 audit_id，upload 在 finish_rclone_upload 成功分支一处收口 |

### kafka（4 条）
| 编号 | 维度 | 位置 | 摘要 → 方案 |
| --- | --- | --- | --- |
| KAFKA-M1 | 并发正确 | `kafkaconn/consume_pool.go:120-127` | consumePoolPut 替换旧条目**不检查 inUse**，并发同形状消费会关掉正在 poll 的 client（驱逐函数跳过 inUse，唯独 put 漏了）→ put 时 old.inUse 则不替换不入池 |
| KAFKA-M2 | 正确性 | `mcp/settings.go:132-170` | LoadSettings 漏恢复 `cursorTtlSecs`/`maxCursorSessions`（Save 却持久化全量），重启静默回默认 → 补两段恢复 + 往返测试 |
| KAFKA-M3 | 性能 | `kafkaconn/messages.go:958-963` | regex 过滤热路径**每记录每通道重复 `regexp.Compile`**（编译比匹配贵），预编译缓存 `matcher.patterns` 未被 match() 使用 → match() 开头查缓存（对齐 :1235-1239 同文件正确写法） |
| KAFKA-M4 | 健壮性 | `kafkaconn/stream.go:204-295,497-511` | stream/start 不预校验 decode/decompression，非法值产生"僵尸会话"占名额 30 分钟（对照一次性消费路径先校验）→ StartStream 建 client 前补 normalize，或失败自注销 |

### 跨插件（8 条）
| 编号 | 维度 | 位置 | 摘要 → 方案 |
| --- | --- | --- | --- |
| X-M1 | mock 引导 | ssh mock.html:13-14（正确范本）vs ldap:12-16、kafka:11-16、files:10 | mock.html 引导三样：ldap/kafka 直挂 App 绕过 themeBridge+存储水合；files 裸开需手拼 `?mock=1` → 统一为 ssh 形态（mockDbxHost + main.ts 双 import） |
| X-M2 | 主题 | 见 X-P4 | applyAppearance 内联覆盖分叉（kafka 独醒）→ 随 X-P4 收敛 |
| X-M3 | 交互 | files App.vue:865(4000ms)、ldap useWorkbenchFeedback.ts:17(3500ms)、kafka App.vue:268(3500ms)、ssh App.vue:1353(reka Toast) | 通知实现与时长四种形态；仅 ssh 有动作按钮/悬停暂停 → 统一 3500/8000 双档常量；中期沉淀 shared 横幅+动作 composable（files 实现最完善可作蓝本） |
| X-M4 | 交互 | ldap ResultTable.vue:114（行内两步）、files ConfirmDialog.vue、kafka TopicsPanel.vue:190（输入同名门禁）、ssh 自绘 | 危险确认原语四种实现（原则一致都禁 window.confirm），力度不齐 → shared 沉淀 ConfirmDialog（两步确认/输入名称两档强度） |
| X-M5 | 组件漂移 | ldap DbxAgGrid.vue(297 行) vs kafka DbxAgGrid.vue(312 行) | 同源 ag-grid 封装两份已漂移（模块注册/多选/列持久化），`.dbx-grid` 主题 CSS 亦两份 → 封装与 `--ag-*` 映射收敛 shared（props 差异化） |
| X-M6 | 组件漂移 | ssh TextPreview.vue:40-42 vs files TextPreview.vue:41 | 同源两份，选区色已分叉（ssh 随明暗双色 / files 固定色），files 的聚焦时序修复未回移 → 观感参数与修复互为回移或抽 shared |
| X-M7 | 类型同步 | ssh env.d.ts:53 有 clipboard*；ldap:70/files:91/kafka:77 无；onContext 声明四种组合无一与真桥完全一致 | env.d.ts 四份类型镜像漂移 → 收敛 `shared/frontend/hostBridge.d.ts`（纯类型零运行时），插件只做域内扩展（杜绝 P2/P3/P5 类漂移的类型层根治） |
| X-M8 | 构建链 | files build.mjs（`<!--` 转义修复）vs ssh/ldap/kafka | files 修了 highlight.js `<!--` 内联坏包坑，其余三家未回移 → 构建脚本收敛 shared/release 单点维护 |

## 五、LOW 级问题（25 条速览）

| 插件 | 编号 | 位置 | 摘要 |
| --- | --- | --- | --- |
| ssh | SSH-L1 | `vault.rs:191-200` | DEK keyfile 先写 tmp 后 chmod，存在 umask（0644）可读窗口 → `OpenOptions::mode(0o600)` 一步创建 |
| ssh | SSH-L2 | `triggers.rs:796-822` | tssh 兼容形态让明文密钥存入宿主连接配置（与 secret binding 红线张力，文档已声明取舍）→ UI 弱化提示引导迁移 |
| ssh | SSH-L3 | `host_key.rs:175-180` | known_hosts 覆写非原子（崩溃可截断全部记录）→ tmp+rename |
| ssh | SSH-L4 | App.vue 9,570 行 / ssh.rs 10,759 行 / mcp.rs 8,363 行 | 三个巨型单文件，维护面持续恶化 → 按面板/域拆分（演进债） |
| ssh | SSH-L5 | `sudo_fs.rs:471-480` | write_file 96KB 块 base64 恰 131,072 字符，贴死 Linux MAX_ARG_STRLEN 零余量 → 块降到 64KB |
| ssh | SSH-L6 | `session_recording.rs:124-129` | 256KB 字节边界截断 + from_utf8_lossy，多字节字符跨边界产生替换符（仅录制保真度） |
| ldap | LDAP-L1 | `service.go:300-317,348-394` | Disconnect 与进行中 WithConn 竞态可致 TCP fd 泄漏 → connectLocked 前后身份双检（对齐 search_sessions.go:199-207） |
| ldap | LDAP-L2 | `policy.go:290-312` + `operations.go:188-190` | 结果屏蔽属性过滤每条目每属性重建 blocked map（O(条目×属性×表长)）→ 顶部构建一次 blockedSet |
| ldap | LDAP-L3 | `dedicated_conn.go:36-84` | 聚合搜索每次独立 dial+StartTLS+bind（有意设计，K-5）；高延迟 LDAPS 可感知慢 → 后续可演进闲时复用专用连接池 |
| ldap | LDAP-L4 | `operations.go:631-637` vs `521-523` | subtreeDeleteAuditRecord 生产死代码，手工重复构造同形状 → 收敛单一来源 |
| ldap | LDAP-L5 | `mcp/stdio.go:515` | 内联池 ids map 的 value 从未读取（死存储）→ 改 `map[string]struct{}` |
| ldap | LDAP-L6 | `mcp/confirm.go:123-129` | crypto/rand 失败退化为纳秒时间戳（一次性令牌可猜）→ 改 panic/fatal 或至少记日志 |
| files | FILES-L1 | `rclone/sync.rs:91-94` + SyncDialog.vue | 工作台 syncDir 镜像删除缺省无 maxDelete 上限（rclone unlimited），仅靠 danger 弹窗 → 默认携带保守 maxDelete 或弹窗加"最多删除 N 个"选项 |
| files | FILES-L2 | `bytes_channel.rs:88-93` | 上传暂存文件未设 0600（同仓 stage_bytes/proc.rs 均已设，红线不一致）→ 照 stage_bytes 补 mode(0o600) |
| files | FILES-L3 | `main.rs:339-354`、`ops.rs:231-252` | files/list recurse=true 无封顶整包入内存；listPaged 10 万上限在全量枚举后才检查 → 复用 listStream 流式或加可配置上限+truncated |
| files | FILES-L4 | `mount/mod.rs:35`、`webdav_gateway.rs:204-232` | WebDAV 网关 GET 全文件缓冲（256MiB/请求，并发成倍放大）→ 增加流式/Range 变体（M2 改进） |
| files | FILES-L5 | `main.rs:1715-1726` | serve/stop 归属校验仅登记表命中时生效，表外 id 可跨连接停 serve → 未命中直接幂等成功 |
| files | FILES-L6 | `main.rs:5057-5060` | listStream 发送失败静默吞掉，宿主断开后仍拉完整个列举 → emit 返回 Result，失败置 cancel |
| kafka | KAFKA-L1 | `style.css:738,134-135` | 状态徽章/图标散装 hex `#10b981`/`#d97706`（README 点名禁用）→ 改 `var(--success)`/`var(--warning)` + color-mix |
| kafka | KAFKA-L2 | `style.css:1-2` | 文件头注释残留 "LDAP workbench styles"（复制模板未改）→ 改头注释 |
| kafka | KAFKA-L3 | `kafkaconn/schema.go:94` | SR REST http.Client 用默认 transport，连接 TLS(CA/skip-verify) 不作用于 SR 通道 → 复用 buildTLSConfig 构建 transport |
| kafka | KAFKA-L4 | GroupsPanel.vue:195-212 | 消费组删除仅普通弹窗，未对齐 topic 删除的输入同名强度 → 前端补输入组名 + 后端 confirmGroup 同级门禁 |
| kafka | KAFKA-L5 | `kafkaconn/audit.go:5` | `Result="warning"` 仅为注释约定全仓无发射点，skip-verify 连接无审计痕迹 → 建连时对 skip-verify 发 warning 审计 |
| 跨插件 | X-L1 | `files/frontend/mock.html:2` | `lang="en"`，其余三家 zh-CN → 统一 zh-CN |
| 跨插件 | X-L2 | `ssh/frontend/src/style.css:16` | 终端字体回退链与其余三家不同（main.ts override 有背书，仅 Host 1.0/mock 兜底分叉）→ 低优先对齐 |

## 六、分维度汇总

### 安全
- **红线全部成立**：凭据 secret binding（四插件 manifest + 内存隔离 + Debug 脱敏 + 审计/日志 grep 全净）、
  两阶段写确认（一次性 token + 参数 hash 绑定 + 过期/失配/重放四态）、read-only 双层门禁、
  ssh host-key TOFU fail-closed、files zip-slip/炸弹守卫、ldap filter 转义链、kafka digest 脱敏与定位字段不截断。
- **残余危险面**：集中在"各自最后一个未建模的惯用法/暴露面"——ssh `xargs`/脚本解释器（SSH-H2）、
  ldap modifyDn 控制字符（LDAP-H1）与聚合无上限（LDAP-H2）、files serve 无鉴权（FILES-H1）与
  rc 凭据经 argv（FILES-M1）、kafka 解压炸弹（KAFKA-H1）。
- **审计完整性**：files 存在写路径审计盲区（FILES-M5）；kafka skip-verify 无审计痕迹（KAFKA-L5）；
  ldap warning 越契约（LDAP-M1）。建议把"每个远端写副作用必落 audit"固化为 smoke 检查项。

### 性能
- 高频模式是**无上限物化进内存**：ldap 搜索聚合（LDAP-H2）、kafka 解压+digest 双字段留存（KAFKA-H1）、
  ssh 录制整文件读（SSH-M2）、files 递归列举与网关缓冲（FILES-L3/L4）。
  建议族内约定统一上限常量与 `truncated` 标志语义。
- **锁与取消**：ssh 连接池全局锁内做网络 I/O（SSH-M3）、files 传输零超时且取消不可中断挂起读（FILES-M2）、
  kafka 消费池 put 竞态（KAFKA-M1）与正则热路径重编译（KAFKA-M3）。
- 族内已做到位的有界机制可作范本：ssh ReplayBuffer/metrics 环/审计轮转、kafka ring buffer 定容、
  files listStream 软超时+并发闸。

### UI 体验一致性
- **做得最好的两项**：公共层零复制（五模块全为相对引用 + 薄 spec，全仓反查无一处重复实现）、
  i18n 七语逐 key 对齐（四插件 0 缺 0 多，AST 脚本验证）。
- **真正的风险是契约漂移**：真桥 API 面（onContext / env 推送 locale）与 env.d.ts、mock 三层
  没有单点对照源，导致 ssh/kafka 幽灵订阅 + mock 幽灵 API（X-P2/P3/P5）。
- **运行时主题四份拷贝 + 三种写法**（X-P4/X-M2），kafka 的"探测-移除"是唯一正确实现。
- 通知/确认/表格/编辑器四类组件各有两种以上形态（X-M3/M4/M5/M6）——功能等价但观感与时长不齐。

## 七、解决方案与修复路线图

### 第 0 步（立即，半天内）
1. **`git add shared/frontend/pluginStorage.ts`（含 README 修改）并提交**（X-P1）——当前干净检出构建全红。
2. ssh：8 处 `window.confirm` 替换为应用内弹窗 + CI lint（SSH-H1）——纯前端小改，修的是"核心功能静默失效"。

### 第 1 步（短期，1-2 个迭代内的高优先安全/稳定性）
3. ssh mcp_safety：`xargs` wrapper 展开 + 脚本解释器 Destructive 化 + 对抗测试（SSH-H2）。
4. kafka：解压上限 LimitReader + valueText 截断（KAFKA-H1）；消费池 put inUse 检查（KAFKA-M1）；settings 往返补字段（KAFKA-M2）。
5. files：serve/start 加 token（FILES-H1）；rc 凭据走环境变量（FILES-M1）；传输读空闲超时（FILES-M2）；Windows 反斜杠根路径归一（FILES-M3）。
6. ldap：modifyDn 控制字符校验（LDAP-H1）；聚合防御上限 + sizeLimit schema 收敛（LDAP-H2）。
7. ssh：SFTP 传输移出连接池锁（SSH-M3）；sudo/readFile 钳制（SSH-M1）；录制流式读（SSH-M2）。

### 第 2 步（中期，一次 shared/frontend 收敛战役）
8. 新建 `shared/frontend/hostBridge.d.ts`（真桥 API 面单点类型）+ `hostThemeRuntime.ts`
   （env/context/locale/theme 订阅归一 + appearance 单点，kafka 策略为准）→ 四插件薄接入，
   同时根治 X-P2/P3/P4 与 M7。
9. 以真桥为准裁剪四份 mock 的幽灵 API，env 推送对齐 onEvent 路径（X-P5）；mock.html 统一 ssh 形态（X-M1）。
10. 组件收敛候选排期：ConfirmDialog（两档强度）、通知横幅 composable（3500/8000 双档）、
    DbxAgGrid 封装与主题 CSS、TextPreview 观感参数、build.mjs 模板（X-M3/M4/M5/M6/M8）。
11. 契约卫生：审计 result 三值对齐（LDAP-M1）、MCP 描述与实现对齐（FILES-M4）、
    audit 写路径全覆盖（FILES-M5/KAFKA-L5）。

### 防回归建议
- CI lint：`window.confirm|onLocaleChange|onContextChange` 在四插件 frontend 内应为 0（env.d.ts 除外）。
- shared/frontend 加一条"mock 形状 = 真桥 API 面"的对照测试或文档检查点（规则 7 的机械化）。
- smoke 增项：远端写副作用必落 audit；解压/聚合路径带上限用例。

## 八、亮点（保持项）

1. **ssh**：密文面纪律系统性闭环（Debug 脱敏 → 事件零载荷 → 命令输出不进错误 → AEAD+AAD 存储 → 审计截断，每环有测试）；门禁五轮对抗测试即规格。
2. **ldap**：凭据红线执行彻底（全链 grep 验证无凭据面）；MCP 写防护纵深（三道防线 + confirmToken 四态）；前端 filter 转义链完整。
3. **files**：出网 URL 校验是表驱动对抗测试的教科书实现（十六进制/裸整数 IPv4、userinfo 走私全覆盖）；零新增 crate 手写解析器仍守住 zip-slip/炸弹守卫；rcd 孤儿进程剩余风险有文档有测试。
4. **kafka**：消费池注释固化了 franz-go v1.20 真集群最小复现的语义知识；actionable errors 体系（错误永远带出路）；ResetGroupOffsets 对"响应缺失按失败处理"的防御性合并。
5. **跨插件**：公共层零复制 + 薄 spec 保底是规则 7 落地的最佳实践；themeSync 首绘即命中 + 语义令牌化的设计质量高；七语 i18n 工程化达标。

## 九、已核查无问题项（抽样声明）

四插件均确认：凭据不落盘不进日志；TLS 校验无静默降级（显式 insecure 均有告警/审计意图）；
MCP read-only 连接写工具不注册且执行期再校验；两阶段确认不可重放/不可换参；无并发 map 裸读写；
goroutine/channel 泄漏未发现（kafka 三处取消路径、files WorkGuard、ldap session cap+TTL 均闭环）；
危险操作均有确认弹窗（无 window.confirm 依赖，ssh 残留除外）。

---
*报告生成：主会话汇总 5 个并发审查 agent 输出，HIGH 级发现经 grep/源码抽查复验；各 agent 原始详述（含完整代码证据）可回溯本仓当日会话记录。*
