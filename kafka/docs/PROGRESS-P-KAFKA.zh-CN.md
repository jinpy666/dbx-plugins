# PROGRESS — DBX Kafka 前端路（P）

> 状态：frontend 路交付（2026-09-05）。
> 唯一工作来源：`docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md` §7（前端实施）+ §4/§5（契约）。
> 本路只写 `kafka/frontend/**` 与本文档，未触碰工作区其他文件。

## 1. 交付范围

蓝本照 `ldap/frontend`（Vue3 + Vite + vitest + vue-tsc，`packageManager:
pnpm@11.24.0`），依赖集与 ldap 完全一致（**零新 npm 依赖**）：vue、
@lucide/vue、@vitejs/plugin-vue、@vue/test-utils、happy-dom、typescript、
vite、vitest、vue-tsc。

### 1.1 脚手架（照抄 ldap，改名）

- `package.json`（name `@xynanan/dbx-kafka-ui`）、`vite.config.ts`、`build.mjs`
  （自包含单文件产物 → `../ui/index.html`，ui 目录规则不变）、`tsconfig.json`
  （types 保持 `["vite/client"]`，与 ldap 基线一致）、`index.html`、`mock.html`、
  `src/main.ts`、`src/env.d.ts`。

### 1.2 lib/

| 文件 | 内容 |
| --- | --- |
| `src/lib/api.ts` | `callKafka<T>(method, params)`（`invoke ?? request` + connectionId 注入）；`kafkaApi` 覆盖 IMPL_PLAN §5.2 全部方法：brokers/list+config、topics/list/describe/create/delete/partitions-update/config-get/config-alter/offsets-list、groups/list/describe/offsets-list/delete/offsets-reset、acls/list/create/delete、messages/produce/consume/export、stream/start/stop/pause/resume/status/messages、presets/list/save/remove、connections/statuses；事件类型 `kafka/stream/messages`（含 bufferSize）、`kafka/stream/error`、`kafka/audit` |
| `src/lib/kafkaModel.ts` | 纯函数：消息二次解码/格式化管线（valueBase64 → 内层 base64 → GZip inflate（浏览器 DecompressionStream；lz4/zstd/snappy 标注降级）→ raw/JSON pretty/hex/BitSet）、`appendStreamRows` 环形上限裁剪、topic 业务评分排序（internal 沉底，对标 tinyrdm kafkaNormalize）、lag 聚合、CSV（RFC 4180）/JSON 导出序列化、Confluent properties 解析（注释/续行/转义）→ 连接表单字段映射、`validateConsumeForm`（§5.3 互斥规则）、partition/partitionOffsets 解析、offsetTime 解析（unix ms/datetime-local/RFC3339） |
| `src/lib/i18n.ts` | 七语（zh-CN/zh-TW/en/es/it/ja/pt-BR），**每语 325 个 key，集合经 spec 断言完全一致** |
| `src/lib/i18n.spec.ts` | 七语完整性守卫（7 locale 集合相等 + 无空值 + locale 家族回退 + 模板替换） |
| `src/lib/kafkaErrors.ts` | `friendlyKafkaError`：门禁类（read-only/allow-delete/confirmTopic）优先，其次 SASL/TLS/网络/超时，未知透传 |
| `src/lib/auditFeed.ts` | 照 ldap 改（默认 action `kafka`） |
| `src/lib/appearance.ts` / `hostTheme.ts` | 照 ldap 抄（宿主外观契约解析 / 1.1 theme 通道适配），后续收敛 shared/frontend 候选 |
| `src/lib/sharedBridge.spec.ts` | shared/frontend 公共层薄 spec（README 约定）：相对引用 `../../../../shared/frontend/binaryEvent` 并断言 binary 双形状归一化。本插件事件为 JSON 载荷、无二进制通道，按规范仍统一走 shared |

### 1.3 App.vue + components/（Phase 1 组件清单）

- `App.vue`：工作台外壳（toolbar 身份/只读·禁删徽章/连接面板入口/刷新、
  七面板 tab 栏、横幅/通知、`waitForHostApi` + ready/request(host.getContext)
  竞速、appearance/theme/locale/context 事件订阅、`kafka/audit` → 审计面板、
  `kafka/stream/*` → StreamPanel.pushEvent 转发、`kafka/connections/statuses`
  读回后端 readOnly/allowDelete 门禁）。
- `TopicTree.vue`：业务 topic 评分排序 + internal（`_` 前缀/后端标记）沉底、
  过滤框、分区数/内部徽章、选中态（selectedTopic 单一来源）。
- `MessagesPanel.vue`：一次性消费表单覆盖 §5.3 全参数——topic、groupId、
  offsetStrategy 五选、offsetTime（datetime/unix ms）、partitions、
  partitionOffsets（`0=100,1:200`）、limit/timeoutMs/maxScanRecords、
  isolationLevel、commit（与过滤互斥、必须 groupId，前端先校验后禁用）、
  filter/keyFilter/valueFilter/headerFilter + matchMode、fieldFilters 编辑器
  （source/path/operator/值/启用开关）、timestampFrom/To、offsetFrom/To、
  decode/decompression；消息表 + 详情抽屉（本地二次 decode/decompression/
  format 切换、valueBase64 完整查看、下载 value）+ JSON/CSV 导出（后端
  `kafka/messages/export`，Blob URL 下载兜底）+ 消费预设（presets 存取）。
- `StreamPanel.vue`：start/stop/pause/resume、收 `kafka/stream/messages` 事件
  实时追加、`kafka/stream/error` 横幅、自动滚动 + 用户上滚暂停、前端展示上限
  1000 行溢出丢最旧并显示 droppedRows 提示、环形缓冲历史分页（Older/Newer →
  `kafka/stream/messages` invoke）、5s 轮询 status。
- `ProducePanel.vue`：key/value/headers（JSON 校验）/partition/count≤1000/
  compression 下拉；发送回显 partition/offset；read_only 整体禁用 + 提示。
- `TopicsPanel.vue`：list（业务排序）/create（partitions/replicationFactor/
  config JSON）/delete（**输 topic 名 confirmTopic 确认**）/扩分区（只增）/
  config get+alter（kv 编辑 + 删除键）/offsets 查询（earliest/latest/自定义
  时间戳）；分区健康视图（leader/replicas/ISR/offline/isHealthy）。
- `GroupsPanel.vue`：组列表（state/protocol/coordinator）+ offsets 表
  （start/end/committed/lag、`hasCommitted:false` → "无已提交数据" 标注）+
  totalLag 聚合徽章 + describe members（assignments）+ reset（earliest/
  latest/timestamp/partitionOffset）+ delete。
- `BrokersPanel.vue`：broker 列表 + config 查看（sensitive 条目掩码）。
- `AclsPanel.vue`：list（过宽过滤前端预检拒绝：必须 resourceName 或
  principal）/create/delete（回显 matched 数）；枚举值为 Kafka 协议术语保持原文。
- `ConnectionsPanel.vue` / `AuditFeedPanel.vue`：照 ldap 改（statuses 增加
  allowDelete 门禁徽标；审计收 `kafka/audit`）。

### 1.4 只读/allowDelete 门禁（前端先禁用 + 提示）

- `readOnly`（宿主 context ∥ `kafka/connections/statuses.readOnly`）→
  工具栏"只读"徽章；produce/commit/topics create·alter·扩分组、groups reset
  等写操作禁用。
- `allowDelete === false`（statuses 字段，缺省不主动禁用、后端仍拒绝）→
  "禁删"徽章；topics/delete（含 confirmTopic 输入确认）、groups/delete、
  acls/delete 禁用。

### 1.5 mock（src/mockDbxHost.ts）

内存假桥，**镜像真实桥当前形状**（与 ldap mock 同面）：`invoke ?? request`、
onEvent/onAppearanceChange/onLocaleChange/onContextChange、decodeBase64/
encodeBase64、workbenchState/clipboard；本插件事件为 JSON 载荷（无 binary 通道）。
覆盖全部 kafka/* 方法：内存集群（2 broker、5 topic 含 2 internal、消费组、
ACL）、produce 追加并回显 offset、consume 按策略/过滤扫描、stream 会话
（定时器发 `kafka/stream/messages` 事件 + ring buffer + pause/resume/status/
messages 分页）、写门禁（ro=1 / nodelete=1 时先发 denied `kafka/audit` 事件再
抛错，镜像后端 policy 分支）。URL 参数：`?theme` `?locale` `?err=1` `?noconn=1`
`?ro=1` `?nodelete=1`。

## 2. 验证证据（真实运行）

环境：`PATH="$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$PATH"`，
`pnpm@11.24.0`。

```
=== pnpm install ===
+ @lucide/vue 1.17.0, vue 3.5.42, @vitejs/plugin-vue 6.0.7, @vue/test-utils 2.5.0,
  happy-dom 15.11.7, typescript 6.0.3, vite 8.0.16, vitest 4.1.11, vue-tsc 3.3.11
Done in 4.4s using pnpm v11.24.0

=== pnpm typecheck ===
$ vue-tsc --noEmit        # 退出码 0，无输出

=== pnpm test ===
 ✓ src/lib/sharedBridge.spec.ts (1 test)
 ✓ src/lib/kafkaModel.spec.ts (24 tests)
 ✓ src/lib/kafkaErrors.spec.ts (3 tests)
 ✓ src/lib/i18n.spec.ts (5 tests)
 ✓ src/components/TopicTree.spec.ts (5 tests)
 Test Files  5 passed (5)
      Tests  38 passed (38)

=== pnpm build ===
✓ 1769 modules transformed.
Wrote self-contained plugin UI to .../kafka/ui/index.html
（268,058 字节，单 <script> 自包含，产物落 ../ui/index.html，ui/ 已 gitignore）
```

### 七语 key 数

- 7 个 locale（zh-CN/zh-TW/en/es/it/ja/pt-BR），**每语 325 key**，扁平化集合
  两两一致（i18n.spec 断言 + 独立脚本复核），无空值。

### 浏览器可视化验证（pnpm dev → /mock.html，Playwright 截图核对后即删）

- 深色 zh-CN 初始渲染：toolbar 身份/徽章、七 tab、topic 树业务排序 +
  internal 沉底、消费表单全参数 ✓
- 选中 order-events → 消费：`已扫描 3 · 命中 3`，消息表分区/offset/时间/key/
  value/headers（trace-id=t-1）✓
- 详情抽屉：JSON 自动识别并 pretty、内层解码/解压/展示格式切换、完整值
  （base64）/下载 value ✓
- 流式：start 后实时追加（stream-1，`已扫描/命中/缓冲 123` 同步）、自动滚动、
  暂停/停止、会话提示 ✓
- 消费组：billing-consumer（Stable/consumer/coordinator 1）、`总 lag: 2`、
  offsets 表（最早/最新/已提交/LAG）、成员 + assignments ✓
- Broker：2 broker 列表 + 配置入口 ✓
- `?ro=1&nodelete=1`：toolbar 只读/禁删徽章、生产面板"只读连接：已禁用生产"、
  Topic 行 扩分区/删除 按钮 disabled（title 提示只读/禁删）✓
- ACL：过滤表单 + "过宽条件会被拒绝"提示、列表空态 ✓
- 验证过程截图与 `.playwright-mcp/` 已全部删除（仓库卫生规约 4）。

## 3. 契约对齐说明

- 方法名/参数/返回形状均按 IMPL_PLAN §5；`enabled` 仅作为 fieldFilters 前端
  行开关，上线载荷剥离（仅启用行下发）。`kafka/stream/messages` 事件消费
  `bufferSize` 字段做 dropped 估算（§5.4/§5.5）。
- 消息二进制保真：valueText 恒 UTF-8 安全预览、valueBase64 恒完整（§5.3），
  抽屉/导出/下载均取 base64 通道。
- 宿主 1.1 特性 optional 降级：appearance 缺失走 theme 通道，再缺失走本地
  规范色板；导出走 Blob URL（宿主 1.0 无 save-file）。
- **遗留提醒（给收口主线）**：前端契约以本文件 + IMPL_PLAN §5 为准；backend
  路（PROGRESS-B）若对 `ConsumeParams`/事件字段做增删，需同步
  `docs/PROTOCOL_KAFKA.zh-CN.md` 并回改 `src/lib/api.ts` 类型与 mock。

## 4. 遗留与风险

1. **GZip 以外解压算法**：浏览器 DecompressionStream 仅支持 gzip/deflate；
   lz4/zstd/snappy 在详情抽屉选择时会展示"不可用"降级提示（消费管线走后端
   decode/decompression，不受影响）。引入 wasm 解压器登记 Phase 2。
2. **消费预设编辑/删除 UI 较简**：删除以每个预设旁的 ✕ 呈现，后续可收敛为
   管理弹层（ldap 同款问题，非阻塞）。
3. **StreamPanel 状态轮询为 5s 定时器**：`kafka/stream/status` 为兜底刷新，
   主要状态靠事件载荷；后端事件若缺 totalScanned 等字段显示为 0，联调时对齐。
4. **ACL 枚举未翻译**（TOPIC/READ/LITERAL 等协议术语保持原文，七语一致），
   如需本地化需扩 key。
5. 工具链备注：spec 不引 node 专有模块（zlib/Buffer），gzip 样本用预生成
   base64 常量（`H4sIAAAAAAAAE6tWylOyMq8FAPicEYIHAAAA` = gzip('{"n":7}')），
   保持 tsconfig types 与 ldap 基线一致、零新依赖。

## 5. Phase 2 商用化（2026-09-05，frontend 路 E）

### 5.1 现象与目标

Phase 1 面板表格为零依赖自绘网格（无列过滤/排序能力有限）、SR（Schema
Registry）/Kerberos/ZK 连接面缺失、监控只有单次快照无趋势与告警。本期按
Phase 2 冻结契约（任务书 11 个 `kafka/schema/*` 方法 + produce/consume/stream
扩展 + statuses 扩展）做商用化补齐，对标 tinyrdm KafkaGrid/SchemasTab/
MonitorTab/ProducerTab/ConnectionDialog 的能力面，视觉维持 DBX 设计系统。

### 5.2 改动清单

**新依赖（仅 1 个，进 pnpm-lock）**

- `ag-grid-community@36.1.0`：当前最新稳定 MIT 社区版（用户点名要求表格
  列过滤检索能力；v33+ Theming API 支持 `--ag-*` CSS 变量定制，无需企业版、
  无需 legacy CSS 主题，正好用 DBX 令牌对齐）。不引 `ag-grid-vue3`，以
  vanilla `createGrid` 薄封装自控（少一个版本强耦合依赖）。

**新增文件**

| 文件 | 内容 |
| --- | --- |
| `src/components/DbxAgGrid.vue` | ag-grid 封装：排序/列内过滤（文本+数值）/分页 + 页大小 localStorage 持久化（`dbx-kafka-grid-pagesize-<tableKey>`）、窄容器（<560px，ResizeObserver）降级 minimal 列集、单行选择（点击选中）、行点击/选择事件、`rowClassRules` 透传（Monitor 阈值行高亮）、ag 内置文案随工作台 locale 七语切换。行 id：VM 带 `id` 用之，否则 WeakMap 按对象身份分配（重复空 id 会导致行合并，见 5.4） |
| `src/lib/kafkaColumns.ts` | 全部表格列定义集中点：messages/groups/groupOffsets/members/acls/topics/partitions/topicOffsets/subjects/schemaVersions/lag 十一组 builder（`t()` 实时取 locale）+ `to*Rows()` VM 映射（预览截断/时间格式化/健康着色/schema 徽章文本）+ `minimalColumns()` + 页大小持久化存取 + ag-grid 内置 chrome 文案七语表（20 键 ×7） |
| `src/lib/kafkaColumns.spec.ts` | 10 用例：列字段/过滤类型/排序断言、VM 映射、minimalColumns、页大小持久化 roundtrip、ag 文案七语键齐 |
| `src/components/SchemasPanel.vue` | SR 面板：subject 表（ag-grid）→ 版本表（ag-grid）→ schema pretty 查看 → 版本对比（`versions/compare` hunks 渲染 add/remove/modify 徽章 + before/after 色块 + summary）、兼容性 get/set（GLOBAL/SUBJECT 作用域显示）、compatibility check 弹层（粘贴候选 schema + JSON 预校验 + isCompatible/messages 回显）、register 弹层（subject/format avro·json/schema JSON 校验）、delete subject/version（**双门禁双确认**：canDelete + 输入名称 + 二次确认页） |
| `src/components/MonitorPanel.vue` | lag 监控：组 + topics 过滤 → per-partition lag 快照表（ag-grid，超阈值行 `dbx-row-alert` 红调高亮）→ 开始/停止采样（interval 钳位 5–60s）→ 总 lag 迷你趋势 SVG（polyline + 阈值虚线）→ 阈值上穿横幅告警（回落再上穿才重复触发，横幅不被逐轮采样清空）→ 监控方案保存/加载/删除（复用 `kafka/presets/*`，`params.type="monitor"` + `monitor` 载荷；MessagesPanel 预设列表过滤 `type!=="monitor"` 互不混显） |

**改造文件**

- `src/lib/api.ts`：+11 个 `kafka/schema/*` 方法（test/subjects·list/versions·list/
  get/versions·compare/compatibility·get/set/check/register/delete/delete·version）
  与类型（`SchemaSubject/SchemaVersionRow/SchemaDetail/SchemaDiffHunk/SchemaDiff/
  SchemaCompatibility(+CheckResult)/SchemaRegisterResult/SchemaCompatibilityLevel/
  SchemaAttach`）；`ConsumeParams.schema?`、`KafkaMessage.schemaId/schemaSubject/
  schemaVersion`、produce 参数 `valueBase64?/keyBase64?/schema?`（`value` 转可选，
  与 valueBase64 二选一）；`KafkaPreset.params` 扩展 `type?:"monitor"` + `monitor?`
  载荷；`KafkaConnectionStatus` + `schemaRegistry/kerberos/connectionSource`；
  `brokersList` 返回 `connectionSource?`。
- `src/lib/kafkaModel.ts`：+`buildPropertyMappings()`（properties → 只读映射行，
  密码/secret 掩码占位 `••••••`，映射目标为 manifest 连接字段名：bootstrap_servers/
  security_protocol/sasl_mechanism（GSSAPI 提示 kerberos_*）/sasl_username/
  sasl_password/kerberos_principal/kerberos_keytab_path/kerberos_service_name/
  sr_url/sr_username/sr_password）。
- 表格 ag-grid 化：`MessagesPanel`（消息表，行点击开详情抽屉）、`GroupsPanel`
  （组表/offsets 表/成员表三张，行选择驱动 describe；reset/delete 收敛为工具栏
  动作作用于选中组）、`TopicsPanel`（topic 表/分区健康表/offsets 表三张 + 选中
  行工具栏动作；offsets 查询策略全量 earliest/latest/max-timestamp/log-start/
  custom）、`AclsPanel`（ACL 表 + 行点击详情抽屉）。均保留列排序/列内过滤/
  分页持久化/窄容器降级。
- 表单扩展：`MessagesPanel`/`StreamPanel` 消费表单加「SR 解码挂载」区（subject
  下拉来自 `schema/subjects/list`，version 空 = latest，format 随 subject formats
  回填，SR 不可达时静默降级不阻断消费）；`ProducePanel` 加 key/value base64
  直发切换（走 `keyBase64`/`valueBase64`）与 schema 挂载。
- `ConnectionsPanel.vue`：连接摘要加 SR/Kerberos/ZK（connectionSource）状态徽标
  （statuses 缺省字段全部 optional 降级）；新增「properties 导入助手」弹层：粘贴
  → `parsePropertiesText` → `buildPropertyMappings` → 只读键值表（属性/值/宿主
  表单字段三列，密码掩码），纯内存不落 localStorage。
- `App.vue`：+schemas/monitor 两个 tab 与面板挂载；MonitorPanel `alert` 事件 →
  顶部告警横幅。
- `src/mockDbxHost.ts`：SR 假数据（order-events-value avro v1/v2 + user-signup-value
  json v1、版本 diff 假算法、全局/subject 兼容性级别、register/delete 真 mutating），
  11 个 schema 方法假实现（set/register 走 guardWrite、delete 走 critical 门禁）；
  produce/consume/stream 支持 base64 载荷与 schema 挂载（消息附
  schemaId/schemaSubject/schemaVersion）；statuses 返回 schemaRegistry/kerberos/
  connectionSource。
- `src/lib/i18n.ts`：+115 键/语（tabs.schemas·monitor、messages SR 挂载 5 键、
  produce base64 2 键、topics offsets 策略 2 键、connections SR·Kerberos·ZK·导入
  助手 15 键、schemas 61 键、monitor 27 键），七语全量人工翻译，`i18n.spec`
  守卫保持绿。

### 5.3 验证证据（真实运行，2026-09-05）

```
=== pnpm install ===
+ ag-grid-community 36.1.0
Done in 2.6s using pnpm v11.24.0

=== pnpm typecheck ===
$ vue-tsc --noEmit        # 退出码 0（TYPECHECK OK）

=== pnpm test ===
 ✓ src/lib/sharedBridge.spec.ts (1 test)
 ✓ src/lib/kafkaModel.spec.ts (24 tests)
 ✓ src/lib/kafkaErrors.spec.ts (3 tests)
 ✓ src/lib/i18n.spec.ts (5 tests)
 ✓ src/lib/kafkaColumns.spec.ts (10 tests)   ← 新增
 ✓ src/components/TopicTree.spec.ts (5 tests)
 Test Files  6 passed (6)
      Tests  48 passed (48)

=== pnpm build ===
✓ Wrote self-contained plugin UI to .../kafka/ui/index.html
（仅 chunk >500kB 提示：ag-grid 体量 + 自包含单文件产物，预期内；
  assetsInlineLimit 10MB / inlineDynamicImports 不变）
```

七语 key 数：**440 键/语 × 7 语**（Phase 1 为 325），集合一致性由
`i18n.spec.ts` 断言 + tsx 脚本复核（`en keys: 440`）。

### 5.4 mock 浏览器走查（pnpm dev → /mock.html，Playwright 截图核对后即删）

- 暗色 zh-CN：ag-grid 消息表渲染 7 列（分区/Offset/时间/Key/Value/Headers/Schema）、
  分页器已本地化（「每页条数： 50 · 1 to 3 of 3」）✓
- **列内过滤**：Value 列头菜单 → 过滤操作符「包含」（本地化）→ 输入 `A-1002` →
  结果 `1 to 1 of 3`（截屏核对后清除）✓
- **页大小持久化**：`dbx-kafka-grid-pagesize-messages=20` 写入 → 重建表格分页器
  显示 20，且跨 theme/locale 会话生效 ✓
- **SchemasPanel**：subject 两行渲染 → 选中 order-events-value → 版本表 v1(101)/
  v2(102) → schema pretty 查看（含 currency 字段）→ 「对比」v1→v2 → hunks 渲染
  「新增」徽章 + line 14/15 变更后绿色块 + 摘要 `+3 -0 ~0`；兼容性 SUBJECT
  BACKWARD 徽章 ✓
- **MonitorPanel**：选 billing-consumer、阈值 1、开始采样 → 每轮 `groups/offsets/
  list` 快照（分区 lag 明细行超阈值红调高亮）、趋势 SVG 蓝线 + 红色虚线阈值线、
  「已采样 6 次」、告警横幅「Lag 超过阈值：总 lag 2 > 1」✓
- **监控方案**：保存 `billing-lag-watch` → 出现在方案下拉 + 删除按钮 +
  「方案已保存」通知 ✓
- **properties 导入助手**：粘贴含 SCRAM+SR 的 properties → 解析出
  bootstrap_servers/sasl_mechanism/sasl_username（kafka-app）/
  **sasl_password（•••••• 已掩码，明文不出现）**/sr_url/sr_username/sr_password
  （掩码）目标字段表 ✓
- **light 主题 ag-grid 可读**：白底/边框/过滤图标/分页器全部随 DBX 令牌切换 ✓
- **`?ro=1` 写门禁**：Schema 面板「注册」「应用（兼容性）」按钮 disabled 且
  title 提示「只读连接：已禁用 schema 写操作」；delete 按钮随 canDelete=false
  禁用 ✓
- 走查中发现并修复 2 个真 bug：① `getRowId` 对无 `id` 字段的 VM 返回空串导致
  subject 表两行合并（改为 WeakMap 稳定 id）；② 采样循环开头 `emit("error","")`
  把上一轮阈值告警横幅清掉（清空挪到 startSampling 一次性执行）。
- 验证截图与 `.playwright-mcp/` 已全部删除（仓库卫生规约 4）。

### 5.5 遗留与风险（Phase 2 增补）

1. **ag-grid 包体**：自包含单文件 UI 体积显著增大（chunk >500kB 提示）；宿主
   webview 加载本地文件，无网络成本，暂不拆包；如需瘦身可评估按面板动态
   import（与 build.mjs `inlineDynamicImports: true` 冲突，需一并调整）。
2. **ag-grid enterprise 专属能力未用**（行分组/透视等），MIT 社区版能力
   （排序/过滤/分页）即满足本期需求；filter 菜单内建文案已七语，日期过滤器
   等长尾文案未覆盖（表格均未启用 date filter）。
3. **SR 解码为后端职责**：前端只负责挂载参数与结果展示（decodeError 透传）；
   avro/json 真编解码、`references` 引用解析联调时与 backend 路对齐
   （PROGRESS-B），如契约有出入回改 `api.ts` 类型 + mock。
4. **MonitorPanel 采样定时器**：切走 tab（v-show）不停止采样（后台继续算
   lag），仅卸载/停止按钮/换连接清理；如宿主对后台 invoke 有限流再收敛为
   隐藏时暂停。
5. **offsets 查询 `log-start`/`max-timestamp`**：mock 返回与 earliest/latest 同
   形数据；真机联调时确认 franz-go 侧 OffsetTime 语义（契约已冻结）。

## 6. AWS Glue SR UI + 消息二次解码补齐（2026-09-05，frontend 路 H）

### 6.1 现象与目标

Phase P 冻结契约（与 backend 路 G 共用）落地前端三件事：① `kafka/schema/*`
全族增加可选 `registry?: "confluent"|"glue"`、`schema/test` 返回 `provider`、
statuses 的 `schemaRegistry` 增加 `provider`——Schemas 面板需要 registry 徽章
与双后端切换；② produce/consume/stream 的 schema 挂载在 provider=glue 时后端
`-32000` 拒绝（消息编解码仅 Confluent wire format，Glue 仅管理面）——前端
schema 挂载区需禁用 + 提示（保留 discoverability）；③ 新 manifest 连接字段
`glue_region/glue_registry_name/glue_auth_mode/glue_access_key_id/
glue_secret_access_key/glue_session_token`——连接摘要展示 Glue 徽标（secret
只显示配置态）。此外消息详情二次解码此前仅支持 gzip（lz4/zstd/snappy 标注
降级），本期以轻量纯 JS 解压器补齐全四种。

### 6.2 改动清单

**新依赖（3 个，均进 pnpm-lock）**

- `fzstd@0.1.1`（MIT）：zstd 解码，纯 JS、零依赖；仅提供解码器（与 Kafka
  生产端 zstd 帧兼容），单测向量用 `zstd` CLI 预生成 base64 常量。
- `snappyjs@0.7.0`（MIT）：snappy 压缩/解压，纯 JS。
- `lz4js@0.2.0`（ISC）：lz4 frame 压缩/解压，纯 JS。
- 三者均为无 wasm 的轻量实现，符合"浏览器端消息详情二次解码"场景；质量
  评估：fzstd/snappyjs 有成熟使用面，lz4js API 简单（frame 格式与 Kafka lz4
  兼容），未发现需替换同类的必要。snappyjs/lz4js 无类型定义，新增
  `src/vendor-decompress.d.ts` 环境声明（仅声明用到的 compress/uncompress）。

**改造文件**

- `src/lib/api.ts`：+`SchemaRegistryProvider`/`SchemaRegistryTestProvider`
  类型；`schemaTest` 返回加 `provider`；11 个 `kafka/schema/*` 方法全部追加
  可选尾参 `registry`（省略 = 连接默认提供方，旧 sidecar 忽略该参数不受影响）；
  `KafkaConnectionStatus.schemaRegistry` 加 `provider?`；
  `SchemaCompatibilityLevel` 联合类型扩展 Glue 枚举
  （`DISABLED/BACKWARD_ALL/FORWARD_ALL/FULL_ALL`）。
- `src/lib/kafkaModel.ts`：删除 `UNSUPPORTED_DECOMPRESSION` 降级表，新增
  `inflateZstd/inflateSnappy/inflateLz4/inflateDecompression`（同步解压 + 带
  算法前缀的 error 降级，不抛异常）；`formatMessageValue` 管线改为
  gzip（DecompressionStream）∥ 其余三种（纯 JS）分发，头部注释同步更新。
- `src/components/SchemasPanel.vue`：顶部 registry 徽章（`schemas.registryLabel`
  + 当前 provider 名）+ 双后端均配置时的 Confluent/AWS Glue 下拉（挂载
  onMounted 逐 provider `schemaTest` 探测；用户未手动切换时跟随 statuses
  provider）；切换即清空选中态并整表重载（registry = 命名空间）；兼容性
  下拉按 registry 切换枚举集（Confluent 7 档 / Glue 8 档）；全部 11 个方法
  调用透传 registry。
- `src/components/ConnectionsPanel.vue`：+`connection` prop（宿主 context
  connection 摘要）；SR 徽标旁 provider=glue 时显示「AWS Glue」徽章；当前
  连接行下渲染 Glue 摘要徽标：region/registryName/authMode/accessKeyId
  （双源取值：connection 直挂 camelCase ∥ `external_config` snake_case，照
  ldap `base_dn` 模式），`glue_secret_access_key`/`glue_session_token` 只显示
  已配置/未配置（值在 external_config 非空 ∥ `connection_secrets` 名单即视为
  已配置，任何来源不展示值）。
- `MessagesPanel/StreamPanel/ProducePanel.vue`：+`srProvider` prop；
  `glueSchemaDisabled` computed（provider=glue）→ schema 挂载 checkbox 与
  subject/version/format 下拉禁用、下方/表单下方七语提示
  （`messages.schemaGlueDisabled`）、`buildSchemaAttach` 直接返回 undefined
  （预置/预设残留挂载也不会下发）、provider 变 glue 时自动取消勾选。
- `App.vue`：`refreshBackendPolicy` 读 `statuses.schemaRegistry.provider` →
  `srProvider` ref 下发四个面板；ConnectionsPanel 传入 connection 摘要。
- `src/mockDbxHost.ts`：fixture subjects 增加 `provider` 字段（confluent 2 个
  + Glue 假 subjects `orders-value`(avro, BACKWARD_ALL)/`payments-value`
  (json, FULL_ALL)）；`resolveProvider`（registry 参数缺省落连接默认 provider）、
  `providerSubjects/findSubject` 按 provider 过滤；`schema/test` 按 registry
  返回 `{success, provider}`；`?glue=1` 模式：statuses.provider=glue、仅 Glue
  subjects、confluent 探测返回 `{success:false, provider:"none"}`、
  produce/consume/stream 带 schema 挂载时抛
  `-32000: schema attach is not supported for AWS Glue Schema Registry…`
  （镜像后端业务错，默认模式双 registry 可切换）；context.connection 增加
  `external_config.glue_*` 与 `connection_secrets: ["glue_secret_access_key"]`
  （secret 只给名字不给值）；新增 `codec-lab` 假 topic（gzip/zstd 固定 base64
  向量 + snappyjs/lz4js compress 现场构造，详情抽屉四种解压走查用）。
- `src/lib/i18n.ts`：+14 键/语（`messages.schemaGlueDisabled`、connections
  `glueBadge/glueRegion/glueRegistryName/glueAuthMode/glueAccessKeyId/
  glueSecretLabel/glueSessionTokenLabel/glueConfigured/glueNotConfigured`、
  schemas `registryLabel/registryConfluent/registryGlue`），七语人工翻译，
  `i18n.spec` 守卫保持绿。

### 6.3 验证证据（真实运行，2026-09-05）

```
=== pnpm install ===
+ fzstd 0.1.1 + lz4js 0.2.0 + snappyjs 0.7.0
Done in 1.3s using pnpm v11.24.0

=== pnpm typecheck ===
$ vue-tsc --noEmit        # 退出码 0，无输出

=== pnpm test ===
 ✓ src/lib/sharedBridge.spec.ts (1 test)
 ✓ src/lib/kafkaErrors.spec.ts (3 tests)
 ✓ src/lib/kafkaModel.spec.ts (28 tests)   ← +4（zstd 固定向量、snappy/lz4
   库内 compress roundtrip、zstd/snappy/lz4 失败降级三算法断言）
 ✓ src/lib/i18n.spec.ts (5 tests)
 ✓ src/lib/kafkaColumns.spec.ts (10 tests)
 ✓ src/components/TopicTree.spec.ts (5 tests)
 Test Files  6 passed (6) / Tests  52 passed (52)

=== pnpm build ===
✓ Wrote self-contained plugin UI to .../kafka/ui/index.html
（chunk >500kB 提示：ag-grid + 三个解压器进自包含单文件，预期内）
```

七语 key 数：**454 键/语 × 7 语**（Phase 2 为 440，+14），`tsx` 脚本逐 locale
计数一致（en/zh-CN/zh-TW/es/it/ja/pt-BR 全部 454），i18n.spec 集合相等断言绿。

### 6.4 mock 浏览器走查（pnpm dev → /mock.html，Playwright 截图核对后即删）

- **registry 切换**（默认模式=双 registry）：Schema 面板顶部「注册表:
  Confluent」徽章 + Confluent/AWS Glue 下拉 → 切 Glue 后列表刷新为
  orders-value(BACKWARD_ALL)/payments-value(FULL_ALL)，选中 subject 版本表/
  schema pretty/兼容性 SUBJECT 徽章均走 glue 通道 ✓
- **Glue 兼容性枚举**：provider=glue 时下拉为
  NONE/DISABLED/BACKWARD/BACKWARD_ALL/FORWARD/FORWARD_ALL/FULL/FULL_ALL
  （DOM 选项全量断言）✓
- **glue 禁用提示**（`?glue=1`）：消息/流式/生产三面板 schema 挂载 checkbox
  disabled + 「AWS Glue 连接：消息编解码仅支持 Confluent wire format…」
  提示（checkbox.disabled DOM 断言 true + 三面板 hint 文案断言）✓
- **连接摘要 Glue 徽标**（`?glue=1`）：连接弹窗显示「Schema Registry 已启用」
  +「AWS Glue」徽章 + 区域 us-east-1/注册表 dbx-kafka-registry/认证
  access_key/Access Key ID 值 + Secret Access Key: 已配置（绿色，取自
  connection_secrets 名单，值不出现）/会话令牌: 未配置（黄色）✓
- **四种解压详情解码**：codec-lab topic 消费 4 条 → 详情抽屉分别选
  gzip→`{"n":7}`、zstd→`{"orderId":"A-1001","amount":42,"currency":"USD"}`、
  snappy→`{"algo":"snappy","ok":true}`、lz4→`{"algo":"lz4","ok":true}`
  （DOM 逐条读取解码文本断言）✓
- **明暗主题**：dark/light 两套下 registry 徽章、切换下拉、ag-grid、连接
  弹窗均随 DBX 令牌渲染，无新造视觉语言 ✓
- 走查截图、`.playwright-mcp/` 与 dev server 已全部清理（仓库卫生规约 4）。

### 6.5 遗留与风险（Phase P 增补）

1. **`?glue=1` 拒绝路径 UI 不可达**：前端禁用挂载后正常操作触发不到 -32000
   （mock 已实现该分支，供契约联调/脚本验证用）。
2. **secret 配置态判定是前端启发**：宿主不下发 secret 值时以
   `connection_secrets` 名单判定「已配置」；若宿主后续提供权威的
   secret-binding 状态字段，应替换该判定（PROGRESS-P 本节为准）。
3. **SchemasPanel 双 provider 探测**：每次挂载发 2 个 `schema/test`；旧
   sidecar 忽略 registry 参数时两路都返回 confluent 成功 → 只显示徽章不出
   切换下拉（optional 降级，符合规则 3）。
4. **fzstd 仅解码**：zstd 压缩仍由生产端/后端负责（Kafka 场景前端只需解码）；
   单测向量由 `zstd` CLI 预生成（常量见 kafkaModel.spec.ts 头注释）。
5. **Glue 兼容性 set/check 语义**：前端按枚举透传；Glue 的 DISABLED 与
   Confluent NONE 语义差异、真机 check 行为待与 backend 路联调确认。

## 7. UI 体验升级轮（2026-09-05 第四轮：L/M/N/P/Q 并发 + 主线收口）

用户反馈驱动的四项改造 + 持续扫描闭环，五路并发（文件归属隔离：i18n 主线预置、
PROGRESS 主线合并）：

### 7.1 交付（按路）

- **L 路（ProducePanel 重构 + CodeMirror）**：引入 CodeMirror 6
  （@codemirror/{state,view,commands,language,lang-json}；理由：monaco ~5MB+worker
  与自包含单文件 UI 冲突，CM6 ~300KB 无 worker）——`CodeEditor.vue` 薄封装
  （行号/wrap/JSON 高亮+错误行 gutter 标记/invalid 红框/disabled 只读，主题全走
  DBX 令牌、明暗跟随）；ProducePanel 竖排大块重构（零 inline style：topic →
  key → value 大编辑器 flex-grow ≥240px → headers → 发送选项栅格 → 成功条 +
  大号主色发送按钮）；i18n 清理 13 个死 key ×7 语 = 91 条；走查修复 2 个真 bug
  （produce-field-full 抢 flex、CM6 gutter lineMarkerChange 缺失致 "!" 标记残留）。
- **M 路（TopicTree 侧栏 + 消费筛选体验）**：侧栏可拖宽（180–480px，
  `dbx.kafka.ui.treeWidth` 记忆，双击重置）/可折叠（40px 竖条，记忆）/`/` 快捷键/
  计数徽章；消费表单五分组卡片（基础/定位常展开，时间与范围/过滤/解码可折叠
  记忆 `dbx.kafka.ui.msgFilters`）；时间选择器（datetime-local step=1 + 「现在」
  + datetime↔unix ms 切换，经既有 offsetTimeToParam，起止倒挂校验禁用消费）；
  fieldFilters 行式编辑器（source/operator/path/value/启用/删除 + 数值校验红框）。
  走查 11 项全过（含拖宽/折叠/记忆/倒挂禁用/明暗/窄容器）。
- **N 路（持续 UI 扫描）**：`docs/UI_SCAN_FINDINGS.zh-CN.md`——12 个走查对象 ×
  视口（720/1280/1440）× 主题 × 参数（ro/glue/err/noconn）矩阵，含键盘与弹层
  路径；发现 P0×0 / P1×4 / P2×15，P1 全部转入修复。
- **P 路（P1 修复）**：P1-1 消费后首屏让位（结果区 flex 食满 + 表单上限 46% +
  smooth 滚动到统计行）；P1-2 抽屉/连接弹窗 Esc 关闭（遮罩 v-if 卸载无残留、
  高层弹窗在场时让位）；P1-3 焦点管理（打开进面板/关闭归还触发元素/Tab 简单
  陷阱，纯逻辑 `decideModalKeydown` 入 kafkaModel + 5 条 spec）。走查 18/18。
- **Q 路（P1-4 修复）**：`positiveInt` number 型 ref 调 `.trim()` 的运行时异常
  （签名改 unknown + String 归一，partition 输入同病一并修）；发送门禁
  （value 空/headers 非法 → disabled + title/aria-label 原因，复用既有七语 key）。
  走查 8 步 0 控制台异常。主线随后把 MessagesPanel/StreamPanel 的同形
  `positiveInt` 一并防御归一（其 ref 为 string 型属预防性加固）。

### 7.2 验证证据

- `pnpm typecheck` 0 错误；`pnpm test` 7 文件 74 用例全绿（+CodeEditor 8、
  +decideModalKeydown 5）；`pnpm build` 产物自包含单文件 UI（ag-grid + CM6）。
- 四路 Playwright 走查（独立会话/端口互不冲突，截图核对后删除）：L 修复 2 bug
  复测通过、M 11 项、P 18/18、Q 8 步 0 异常；N 二轮复核确认 P1 修复落地。
- 收口 `bash scripts/test.sh` 全绿（前端三件套 + UI 走查 + go + package +
  容器 smoke）。

### 7.3 遗留

- P2×15 见 `docs/UI_SCAN_FINDINGS.zh-CN.md`（后续打磨 backlog：错误文案透传
  原文、ag-grid 焦点指示、Stream 分页按钮热区、抽屉标题语义等）。
- 连接弹窗内「导入助手」子弹层 Esc 会整层关闭（ConnectionsPanel 不在本轮
  授权范围）；secret 配置态判定仍是前端启发（宿主出权威 binding 状态字段后替换）。

## 8. UI 持续优化轮·五（2026-09-05：R/S/T 三路并发 + 主线收口）

### 8.1 交付（按路）

- **R 路（消息页布局压缩 + 大数据量防护）**：消费表单默认收起为一行摘要条
  （chips：topic/策略/上限/过滤数/groupId/decode + mini 消费按钮；消费成功自动
  收起、展开态记忆 `dbx.kafka.ui.msgFormOpen`），表格成为主体（1280×800 首屏
  ~25 行可见）；性能四件套——`capRows` 内存上限 10000 行裁头部（裁剪徽章
  uiRowsCapped）、行数组 shallowRef+triggerRef（5000 行不走深响应代理）、
  ag-grid 一次性批量 rowData 提交、详情抽屉 value 截断预览（头尾 16384 字符
  + ⋯ N chars ⋯，512KB 不再整段塞 DOM）；「跳到最新」兼容 ag-grid v36 三代
  viewport 类名。`?big=1` 压测：5000 行 consume 落地 93ms、20 帧滚动 154ms、
  longtask=0、pageerror=0。
- **S 路（全局 P2 打磨批，8 项）**：错误文案本地化归一（kafkaErrors 扩充
  连接类规则，Stream/Connections/Groups 错误点改走 friendlyKafkaError）；
  Topics 表与侧栏排序统一（sortTopics 同源）；Stream 分页按钮热区 36×20→52×32；
  ag-grid 单元格键盘焦点指示（主题令牌 outline，明暗两套）；导入助手子弹层
  Esc 分层关闭；只读/Glue 禁用态统一视觉（grayscale/opacity/not-allowed +
  title）；ACL 空态引导；Schema 兼容级别徽标语义化。
- **T 路（启动/加载性能 + 大数据压测）**：App 层懒挂载（visited set：首访才
  v-if 实例化 + 已访问 v-show 保状态）+ 7 个重面板 defineAsyncComponent；
  stream 事件背压（面板不可见时有界队列 800 丢最旧，切回按序补发）；
  `?big=1` 压测 fixture（500 topics/5000 消息/200 组）；build.mjs 换
  codeSplitting:false（产物 byte 级一致、deprecation 告警消除）。实测：
  产物 FCP 364→124ms、tab 就绪 384→119ms、DOM 788→221、heap 18.2→13.4MB；
  切 tab 10 轮 heap delta=0；CodeMirror 组件级懒加载经评估**回退**
  （spec 同步断言 + 单文件产物动态 import 被内联提前求值，无净收益，
  模块链推迟由 Produce 面板异步化达成——produce 首挂 145ms 就绪）。
- **主线接线**：groups.state* 六态七语预置 + kafkaColumns 状态列
  valueFormatter 本地化（Stable/Empty/PreparingRebalance/…，未映射枚举原文
  兜底）；确认 TopicTree 错误已统一走 showError→friendlyKafkaError（S 报告的
  英文原文属未命中规则兜底，规则已扩充）。

### 8.2 验证证据

- `pnpm typecheck` 0 错误；`pnpm test` 79/79 绿（+capRows/truncatedValuePreview
  等 5 例）；收口 `bash scripts/test.sh` 全绿（含 pnpm build 产物 1.78MB 自包含、
  UI 走查 2/2、容器 smoke 12 场景 10 PASS/0 FAIL/2 SKIP）。
- 三路走查（R：big 模式摘要条/裁剪/截断/记忆；S：逐项程序化断言 + 明暗/ro/glue
  矩阵；T：性能指标前后对比 + 10 轮切 tab 零泄漏）截图核对后均已删除。

### 8.3 遗留

- 「跳到最新」当前滚动分页视口，跳末页需 DbxAgGrid 暴露 gridApi（跨组件契约，
  下一轮）；Monitor 采样在面板内部降频未做（App 层已背压 stream 事件侧）；
  App 层缓冲丢弃为静默计数；P2-2 错误横幅遮挡 tab 栏、P2-11 light 工具栏泛红、
  P2-13 窄视口死空间（均在 App.vue/style.css，待小修轮）。

## 主题令牌桥（2026-09-05）

- 接入 `shared/frontend/themeSync.ts`：`main.ts` 挂载前 `installHostThemeBridge()`，
  插件变量桥接宿主 `--color-*` 令牌——首绘即命中宿主主题（不再等 init 后 JS 回写），
  主题切换自动跟随，primary/radius/字体纳入同步面。宿主无令牌（mock/旧宿主）回退
  暗色规范值，行为不变。
- 验证：`vue-tsc` 0 错；`vitest run` 8 文件 82 用例全绿（含新增
  `themeSync.spec.ts` 薄 spec）；v0.1.4 发版。
