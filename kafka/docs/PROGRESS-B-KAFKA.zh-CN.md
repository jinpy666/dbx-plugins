# B-KAFKA 路交付报告（io.dbx.kafka backend / Phase 1 全量）

> 路线：IMPL_PLAN_DBX_KAFKA.zh-CN.md §9 A 路 backend —— tinyrdm Kafka
> 实现（franz-go + kadm）重写为 DBX Go sidecar（stdio-jsonl，与 ldap 同构）。
> 范围：仅 `kafka/backend/**` 与本文件；`kafka/frontend/**`、manifest、
> scripts 由并行路负责，未改动。无 git commit/push。

## 0. 验证终值

| 套件 | 结果 |
|---|---|
| `CGO_ENABLED=0 go vet ./...` | ✅ 0 告警 |
| `CGO_ENABLED=0 go test ./...` | **74 passed / 0 failed**（`go test -count=1 -v` 计数） |
| 方法注册 | **34/34**（manifest §5.2 方法表全量，缺一 smoke 会 FAIL 的项无遗漏） |
| manifest 契约测试 | **真实 PASS 非 SKIP**（C 路 manifest.json 已在位，字段/binding/七语全部对齐） |
| 依赖 | franz-go v1.20.7 + kadm v1.17.2（tinyrdm 同款版本，公网拉取成功进 go.sum；CGO_ENABLED=0） |
| 二进制构建 | 未执行（按约定收口统一跑 build.sh） |

复现命令：

```bash
cd /Users/Jinpy/btroot/dbx-plugins/kafka/backend
CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...
# ok  io.dbx.kafka.plugin/internal/kafkaconn
# ok  io.dbx.kafka.plugin/internal/lifecycle
# ok  io.dbx.kafka.plugin/internal/store
```

## 1. 交付总览

```
kafka/backend/
├── go.mod / go.sum              # module io.dbx.kafka.plugin，go 1.24.0；SDK replace 照 ldap
├── main.go                      # SDK server + 34 方法 switch + 审计/流式事件 emitter 注入
└── internal/
    ├── lifecycle/               # 照 ldap 抄改（bootstrap textarea 换行+逗号双拆）
    ├── store/                   # 照 ldap 抄改（DefaultDirName=io.dbx.kafka，audit.jsonl）
    └── kafkaconn/
        ├── types.go             # §5 契约类型（camelCase；消息二进制保真形状）
        ├── client.go            # TLS/SASL 纯构建 + admin client 指纹缓存 + per-request 消费 client
        ├── service.go           # 连接表生命周期 + 预设 + 审计回调 + 状态快照
        ├── topics.go            # brokers 2 方法 + topics 8 方法（含 isHealthy 分区健康）
        ├── groups.go            # groups 5 方法（offsets/reset 自实现，kadm v1.17 无内建）
        ├── acls.go              # acls 3 方法（builder 过滤；过宽拒绝）
        ├── messages.go          # produce/consume/export + 过滤引擎 + 解码解压 + CSV/JSON
        ├── stream.go            # ring buffer 10000 / 会话上限 20 / 30min 回收 / 200ms·50 节流 emit
        ├── policy.go            # read_only × allow_delete 与门 + confirmTopic 守卫
        ├── audit.go             # AuditRecord（main 落盘 + kafka/audit 事件）
        ├── helpers.go           # 归一化与 mutation 结果映射
        └── 7 个 *_test.go + manifest_contract_test.go
```

## 2. 方法注册清单（34，与 IMPL_PLAN §5.2 一致）

- 生命周期：`connection/test`、`connection/connect`、`connection/disconnect`
- brokers：`kafka/brokers/list`、`kafka/brokers/config`
- topics：`kafka/topics/list|describe|create|delete|partitions/update|config/get|config/alter|offsets/list`
- groups：`kafka/groups/list|describe|offsets/list|delete|offsets/reset`
- acls：`kafka/acls/list|create|delete`
- messages：`kafka/messages/produce|consume|export`
- stream：`kafka/stream/start|stop|pause|resume|status|messages`
- presets：`kafka/presets/list|save|remove`
- 全局：`kafka/connections/statuses`

未匹配臂统一 `dbxpluginsdk.MethodNotFound`（-32601）；参数错 -32602、业务错
-32000（blocked 语义经消息文案携带，与 ldap 一致）。

## 3. 关键实现决策（现象 → 改动 → 验证）

### ① 客户端连接复用（tinyrdm 每调用重建 client 的补齐）
- **现象**：tinyrdm `newKafkaClient` 每次调用新建 + Close；管理面轮询浪费。
- **改动**：admin 类调用（brokers/topics/groups/acls/offsets）复用
  `connEntry.client`，指纹 = SHA256(bootstrap+securityProtocol+SASL 机制/
  用户名/密码+CA/cert/key+insecure+clientID)，指纹失效重建；同一连接操作经
  `entry.mu` 串行。consume/stream 携带 per-request 消费 opts，仍每次新建
  client（`consumeClient`）用完即关——避免 ConsumePartitions 等选项污染
  共享通道。
- **验证**：`TestFingerprintStableAndSensitive`（稳定 + 密码/bootstrap 变化
  触发失效）；`TestSeedBrokersFallback`（bootstrap 缺失时 runtime.host:port
  兜底，任务书"拨号支持 runtime.host:port 语义"落地）。

### ② 二进制保真（tinyrdm `string(record.Value)` 已知 bug）
- **改动**：消息形状 `valueText` 恒为 UTF-8 安全预览（非法字节替换
  U+FFFD）、`valueBase64` 恒完整（512KB 上限截断并置 `truncated:true`）；
  key 合法 UTF-8 走 `key` 字段，否则 `keyBase64`；headers 值做 UTF-8 安全
  替换。
- **验证**：`TestMessageBinaryFidelity`（恶意二进制样本 roundtrip
  valueBase64/keyBase64 逐字节相等）；`TestMessageTruncationAtLimit`；
  `TestMessageTextKeyUsesKeyField`；导出 JSON 断言 valueBase64 原样透传
  （`TestExportJSON`）。

### ③ consume 全参数（§5.3）
- **改动**：5 种 offset 策略（latest/earliest/committed/timestamp/offset）、
  per-partition 精确 seek、`commit × 过滤` 互斥（含 fieldFilters/范围过滤）、
  `partitions × groupId` 互斥、matchMode 四态、fieldFilters 七 source 十
  operator（含 JSON path `$.a.b[0].c` 与 gt/gte/lt/lte 数值比较）、base64
  二次解码 + gzip/lz4/zstd/snappy 解压（snappy block/framed 双兜底）、
  scanned/matched/limited/hasMore/nextPartitionOffsets、limit 默认 100、
  maxScanRecords 默认 max(1000, limit×10)。
- **验证**：`consume_params_test.go`（互斥矩阵 11 case + buildConsumeOpts
  缺 offset 报错）；`filter_test.go`（matchMode/通道/字段过滤/JSON path）；
  `codec_test.go`（四种解压 roundtrip + base64+gzip 组合）。

### ④ 流式会话（§5.5）
- **改动**：ring buffer 10000（满覆盖最旧）、会话上限 20、空闲 30min 回收
  （`EvictIdle`，连接断开/重连即 `StopAllForConnection`）、200ms/批 50 节流
  emit `kafka/stream/messages`、fetch 错误指数退避 500ms→30s、pause 时消费
  继续入 ring 仅停推送、read_only 禁 commit（StartStream 校验层拒绝）。
  事件经 `StreamEmitter` 接口注入（main 适配 SDK emitter），kafkaconn 不
  依赖 SDK。
- **验证**：`ringbuffer_test.go`（覆盖最旧、跨环绕分页连续、拷贝语义、
  回收阈值、常量契约 20/10000/30min/200ms/50）。

### ⑤ 安全策略与审计（§6）
- **改动**：`ensureWriteAllowed`（read_only 拒 produce/create/alter/reset/
  ACL 写）、`ensureDeleteAllowed`（read_only ∥ !allow_delete 与门拒
  topics/groups/acls delete）、`ensureTopicDeleteConfirm`（单 topic
  confirmTopic 同名、多 topic confirmTopics 逐一对齐）。写操作成功/拒绝均
  走 `emitAudit` → audit.jsonl（success→ok、blocked→denied）+ `kafka/audit`
  事件；Target 只含资源名。
- **验证**：`policy_test.go`（2×2 门禁矩阵 + confirmTopic 四态）；
  `TestAuditCallback`（含"审计记录不含凭据标记"断言）。

### ⑥ kafka/groups/offsets/reset（host 补齐能力，tinyrdm 无）
- **改动**：kadm v1.17.2 无内建 OffsetReset，基于 `FetchOffsets` +
  `CommitOffsets` 自实现：earliest/latest/timestamp 用 ListOffsets 后提交；
  partitionOffset 用请求 `partitionOffsets{topic:{partition:offset}}` 直接
  构造（leaderEpoch=-1）。逐分区返回 `rows[]{topic,partition,ok,error}`。
  只读策略下拒绝（写操作）。
- **验证**：`TestResetModeNormalization`；reset 主流程属连网路径，留待
  smoke 容器场景覆盖（见 §5 遗留）。

### ⑦ groups/offsets/list Option 语义
- **改动**：`hasCommitted` 区分"组从未提交 offset"（FetchOffsets 结果为空
  → false）与零 lag；行值 = start/end/committed 三表合并，lag= end-
  committed（未提交时视作全量未消费，committed=-1）。
- **验证**：`TestGroupLag`；`groupOffsetRows` 属连网路径，容器场景覆盖。

## 4. 改动清单

新增（全部为本路范围内新文件）：

- `kafka/backend/go.mod`、`go.sum`
- `kafka/backend/main.go`
- `kafka/backend/internal/lifecycle/lifecycle.go` + `lifecycle_test.go`
- `kafka/backend/internal/store/store.go` + `store_test.go`
- `kafka/backend/internal/kafkaconn/`：`types.go`、`client.go`、
  `service.go`、`topics.go`、`groups.go`、`acls.go`、`messages.go`、
  `stream.go`、`policy.go`、`audit.go`、`helpers.go`；测试
  `client_test.go`、`consume_params_test.go`、`filter_test.go`、
  `codec_test.go`、`export_test.go`、`policy_test.go`、`ringbuffer_test.go`、
  `helpers_test.go`、`service_test.go`、`manifest_contract_test.go`
- `kafka/docs/PROGRESS-B-KAFKA.zh-CN.md`（本文件）

依赖取值面（§3 白名单内）：franz-go（kgo/kadm/kmsg/sasl plain+scram）、
google/uuid、klauspost/compress（zstd/snappy，franz-go 同源）、
pierrec/lz4/v4。无 sarama、无 gokrb5（Kerberos 按契约 Phase 2）。

## 5. 遗留与风险

1. **连网路径未真机验证**：单测全部纯解析（无容器依赖）；produce/consume/
   stream/reset 等真实 Kafka 交互由 smoke（`dbx-kafka-test` KRaft 容器）与
   收口阶段覆盖，本路未启动容器。
2. **`kafka/groups/offsets/reset` 的 partitionOffsets 形状**：实现为
   `{topic: {partition: offset}}`；IMPL_PLAN §5.2 未细化到分区维度，收口时
   需与 C 路 `PROTOCOL_KAFKA.zh-CN.md` 核对，若协议定义为扁平
   `{partition: offset}` 需同步调整（改动点集中在
   `GroupOffsetResetRequest` + `ResetGroupOffsets`）。
3. **stream 事件与宿主桥的实测**：`kafka/stream/messages` 为 JSON 载荷
   事件，`pluginHandler` 的 emitter 引用沿用 ldap 的"Serve 期间单例"模式；
   真机多事件流背压（ring 满 + 前端 droppedRows）需 e2e 复核。
4. **admin client 复用在断线场景**：共享 client 断线后 franz-go 自带重连，
   未做"失败重建一次"的 ldap WithConn 同构逻辑（kgo client 常驻自愈，
   语义等价）；若 smoke 发现长断连场景状态不刷新，再补指纹强制重建入口。
5. **`kafka/messages/produce` 的 value 为文本字段**（契约 §5.2 形状）；
   二进制生产（base64 入参）契约未定义，Phase 2 如需可加 `valueBase64`
   入参字段。
6. manifest 契约测试当前真实 PASS（manifest 已在位）；若 manifest 后续
   调整字段，以本测试为守卫（文件缺失时才 Skip）。
