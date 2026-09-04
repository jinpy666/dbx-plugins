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
