# IMPL PLAN — DBX Kafka 插件（io.dbx.kafka）

> 状态：Phase 1（基础版）+ Phase 2（商用化）已实施，收口验证见 §11。
> 决策记录：本文件是 kafka 插件**唯一工作来源**。工作区 AGENTS.md 原有
> "聚焦三插件、不做任何新插件" 约束，经用户于 2026-09-05 明确指令新增
> kafka 插件而解除；根 README.md / AGENTS.md 随本次任务同步修订登记。
> 2026-09-05 目标升级：完整覆盖 tinyrdm 特性 + host 特有能力（取长补短）、
> 商用级鉴权/加密矩阵、ag-grid 表格过滤检索、docker 覆盖测试——即本文件
> §0.2 的 Phase 2 项全部落地，统一记入 §11。

## 0. 目标与非目标

### 0.1 目标

从 tiny-rdm（本地 fork `mcpctl`，源码 `/Users/Jinpy/GolandProjects/tiny-rdm`）的
Kafka 实现重写为 DBX 插件：

- **Go sidecar**（stdio-jsonl，与 `ldap/` 同构），Kafka 客户端用
  `github.com/twmb/franz-go` + `pkg/kadm`（tinyrdm 同款库，迁移成本最低）。
- **取 tinyrdm 之长**：消费语义（5 种 offset 策略、per-partition 精确 seek、
  commit 与过滤互斥、续读游标）、Go 侧字段级过滤（三通道 + matchMode +
  JSON path + 数值比较）、base64/四种解压解码、流式消费会话（ring buffer、
  暂停/恢复、空闲回收）、导出 JSON/CSV、Confluent properties 导入（前端）、
  高危操作分级门禁。
- **补 tinyrdm 之短**：二进制值保真（tinyrdm `string(record.Value)` 会损坏
  二进制消息；本插件 value 一律 `base64 保真 + text 预览` 双字段）；
  **客户端连接复用**（tinyrdm 每调用重建 client；本插件按 connectionId
  缓存 client，指纹失效重建）。
- **参考 host 补齐**（host 的 Kafka 是 Java agent，消息浏览仅 peek ≤100 条、
  无实时消费/过滤/导出——插件正好补齐）：消费组 lag 快照（Option 语义区分
  "无数据/零 lag"）、topic 分区健康视图（leader/replicas/ISR/offline）、
  消费组 offset 重置（host 有 `mq_reset_consumer_group_offsets`，tinyrdm 无）。
- **不重复宿主**（M0 红线）：连接 profile 持久化、凭据 secret binding、
  SSH 隧道/代理传输层（sidecar 经 `runtime.host:port` 拨号）、read_only
  治理与审计基线全部走宿主。

### 0.2 非目标

原 Phase 2 项（Schema Registry、Kerberos、ZooKeeper 发现）已于 2026-09-05
商用化轮次全部落地（见 §11）。当前剩余非目标（Phase 3 登记）：

- OAUTHBEARER / AWS MSK IAM 鉴权（tinyrdm/host 均无；sidecar 无 token 回调通道）。
  AWS Glue SR 管理面已于 2026-09-05 第三轮落地（见 §11.5），tinyrdm 双 SR 后端
  全覆盖；aws-profile 凭据模式由 default 链覆盖，无独立开关。
- PROTOBUF 载荷编解码（SR 场景，明确报未实现）。
- 幂等/事务生产参数、ACL 之外的 quota/reassignment/log dir。
- 与宿主 MQ 控制台（topics/groups/ACL 治理面）的互通。

## 1. 三方功能对标（范围依据）

| 能力 | tinyrdm | host | 本插件 Phase 1 |
| --- | --- | --- | --- |
| 连接 profile 持久化 | sqlite 自管 | 宿主 ConnectionConfig | **宿主**（manifest connection-provider） |
| 凭据存储 | sqlite 明文 | 加密 secret | **宿主 secret binding** |
| SSH 隧道/代理 | 自建 dialer | 宿主传输层 | **宿主**（sidecar 经 runtime.host:port） |
| 认证 | PLAIN/SCRAM-256/512/Kerberos | PLAIN/SCRAM/Kerberos(Java) | PLAIN/SCRAM-256/SCRAM-512 + TLS；Kerberos Phase 2 |
| broker 列表/config | ✅ | ✅(describe_cluster) | ✅ brokers/list + brokers/config |
| topics 列表/创建/删除/扩分区/配置 | ✅（副本因子可配） | ✅（副本硬编码 1） | ✅（replicationFactor 可配） |
| 分区元数据与健康 | ✅ leader/ISR | ✅ partitionStats | ✅ + isHealthy(isr/replicas) |
| offset 查询 | earliest/latest/max-timestamp/按时间 | begin/end | earliest/latest/按时间戳 |
| 消息生产 | ✅ 批量≤1000/headers/压缩/指定分区 | ✅ 单条 | ✅ 对齐 tinyrdm |
| 消息消费（一次性） | ✅ 5 策略+过滤+解码+导出 | peek ≤100 无过滤 | ✅ 对齐 tinyrdm |
| 消息消费（流式） | ✅ ring buffer 会话 | ❌ | ✅（sidecar 事件通道） |
| 二进制保真 | ❌（string 直转） | base64 | ✅ base64+text 双字段 |
| 消费组 list/describe/lag | ✅ | ✅ 快照+Option 语义 | ✅ 两者合并 |
| 消费组删除/offset 重置 | 删除✅/重置❌ | 重置✅ | ✅ 都做 |
| ACL | ✅ 全枚举 | grant/revoke 简化 | ✅ 对齐 tinyrdm |
| 导出 | JSON/CSV | ❌ | ✅ JSON/CSV |
| 只读/写门禁 | 审批弹窗（进程内） | read_only+生产确认 | **宿主 read_only + allowDelete 策略** |

## 2. 仓库结构

```
kafka/
├── manifest.json              # id=io.dbx.kafka，connection-provider database_type=kafka
├── dbx-plugin.toml            # language=go，binary=dbx-plugin-kafka，include=[assets,ui]
├── README.md
├── .gitignore                 # backend/bin、ui/、dist 等（照 ldap/.gitignore）
├── assets/plugin.svg          # 64×64 图标
├── docker-compose.kafka-test.yml   # apache/kafka KRaft 单机，dbx-kafka-test:9092
├── backend/
│   ├── go.mod / go.sum        # module io.dbx.kafka.plugin；franz-go+kadm+uuid；SDK replace 照 ldap
│   ├── main.go                # SDK server + 方法 switch（同 ldap/main.go 模式）
│   └── internal/
│       ├── kafkaconn/         # 领域包：types.go / service.go / client.go(拨号+SASL+TLS+缓存) /
│       │                      #   topics.go / groups.go / messages.go(produce+consume+filter) /
│       │                      #   stream.go(流式会话) / acls.go / policy.go / audit.go
│       │                      #   + 每文件 *_test.go + manifest_contract_test.go
│       ├── lifecycle/         # 照抄 ldap/internal/lifecycle（provider id/databaseType 改 kafka）
│       └── store/             # 照抄 ldap/internal/store（DefaultDirName="io.dbx.kafka"，audit.jsonl）
├── frontend/                  # Vue3+Vite+vitest，目录同 ldap/frontend
├── scripts/
│   ├── build.sh / test.sh / ui_test.mjs
│   ├── sidecar_client_jsonl.py / smoke_test.py / smoke_container.py
│   └── kafka-seed/            # 建测试 topic 脚本（无凭据）
└── docs/
    ├── IMPL_PLAN_DBX_KAFKA.zh-CN.md   # 本文件
    ├── PROTOCOL_KAFKA.zh-CN.md        # 协议文档（§5 展开；新方法必须同步此处）
    └── PROGRESS-B-KAFKA.zh-CN.md      # backend 路（frontend 路另有 P 文档）
```

## 3. Go 依赖

- `github.com/twmb/franz-go`（kgo）、`github.com/twmb/franz-go/pkg/kadm`
- `github.com/google/uuid`、SDK `github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk`
  （replace 照 ldap 指向 `../../../dbx-plugin-host-worktree/plugins/sdk/go/dbx-plugin-sdk`）
- 压缩解压：franz-go 自带 gzip/snappy/lz4/zstd 依赖可直接复用
- 全部进 go.sum；`CGO_ENABLED=0`

## 4. manifest 贡献点

`connection-provider`（id `io.dbx.kafka.connection`，database_type `kafka`，
capabilities `["test","connect","disconnect"]`，workbench `io.dbx.kafka.workbench`）字段：

| key | label | type | binding | 说明 |
| --- | --- | --- | --- | --- |
| display_name | 连接名 | text | name | required，默认 "Kafka cluster" |
| bootstrap_servers | Bootstrap servers | textarea | config | required，`host:port` 逗号/换行分隔 |
| security_protocol | Security protocol | select(PLAINTEXT/SSL/SASL_PLAINTEXT/SASL_SSL) | config | 默认 PLAINTEXT |
| sasl_mechanism | SASL mechanism | select(PLAIN/SCRAM-SHA-256/SCRAM-SHA-512) | config | visible_when security_protocol 含 SASL |
| sasl_username / sasl_password | 用户名/密码 | text/password | config / secret | visible_when 含 SASL |
| tls_ca_cert | CA 证书 (PEM) | textarea | config | visible_when 含 SSL |
| tls_client_cert / tls_client_key | 客户端证书/私钥 | textarea/password | config / secret | visible_when 含 SSL |
| tls_insecure_skip_verify | 跳过 TLS 校验 | boolean | config | 默认 false，visible_when 含 SSL |
| client_id | Client ID | text | config | 可选 |
| read_only | 只读模式 | boolean | config | 默认 true |
| allow_delete | 允许删除类操作 | boolean | config | 默认 false；read_only 下强制无效 |

七语 localizations 全量（zh-CN/zh-TW/en/es/it/ja/pt-BR），含每个字段的
label/description。`engines: { dbx: ">=0.5.77", host_api: ">=1.0.0" }`，
permissions `["host.events","host.workbench"]`。

## 5. Sidecar 方法契约

公共约定：stdio-jsonl（SDK 帧）、方法 `<域>/<动作>`、字段 camelCase、
必填 `connectionId`、参数错 `-32602`、业务错 `-32000`、未注册 `-32601`。
响应统一 `{ ok: true, data }`；错误走 PluginError。**完整字段表见
`docs/PROTOCOL_KAFKA.zh-CN.md`（实现必须与其同步）**。

### 5.1 生命周期（SDK/ldap 同款）

- `connection/test` / `connection/connect` / `connection/disconnect`

### 5.2 领域方法

| 方法 | 请求要点 | 返回要点 |
| --- | --- | --- |
| `kafka/brokers/list` | — | `brokers[]{nodeId,host,port,rack}` |
| `kafka/brokers/config` | `brokerId` | `entries[]{name,value,source,sensitive,isDefault}` |
| `kafka/topics/list` | `includeInternal?` | `topics[]{name,topicId,isInternal,partitionCount,replicationFactor,error?}` |
| `kafka/topics/describe` | `topic` | `partitions[]{partition,leader,leaderEpoch,replicas[],isr[],offlineReplicas[],isHealthy}` |
| `kafka/topics/create` | `topics[]`, `partitions`, `replicationFactor`, `config?` | 每条 `results[]{topic,ok,error}` |
| `kafka/topics/delete` | `topics[]` | 同上（critical 门禁） |
| `kafka/topics/partitions/update` | `partitions`（map topic→新分区数，只增） | 同上 |
| `kafka/topics/config/get` | `topic` | `entries[]` 同 brokers/config |
| `kafka/topics/config/alter` | `topic`, `config{}`, `deleteKeys[]` | 同上 |
| `kafka/topics/offsets/list` | `topics[]`, `offsetTime?`(earliest/latest/RFC3339/unix ms) | `rows[]{topic,partition,offset,timestamp,leaderEpoch}` |
| `kafka/groups/list` | — | `groups[]{group,state,protocolType,coordinator}` |
| `kafka/groups/describe` | `group` | `members[]{memberId,instanceId,clientId,clientHost,assignments{topic:[]partition}}` |
| `kafka/groups/offsets/list` | `group`, `topics?`(空=committed 全量) | `rows[]{topic,partition,startOffset,endOffset,committedOffset,lag}` + `totalLag`（Option 语义：无 committed 数据→`hasCommitted:false`，与零 lag 区分） |
| `kafka/groups/delete` | `group` | —（critical 门禁） |
| `kafka/groups/offsets/reset` | `group`, `topics[]`, `resetTo`(earliest/latest/timestamp/partitionOffset), `timestampMs?`, `partitionOffsets?` | `rows[]{topic,partition,ok,error}` |
| `kafka/acls/list` | `filter{}`（拒绝过宽） | `acls[]{resourceType,resourceName,patternType,principal,host,operation,permission}` |
| `kafka/acls/create` | `acl{}` | — |
| `kafka/acls/delete` | `filter{}` | `matched[]` |
| `kafka/messages/produce` | `topic`, `key?`, `value`, `headers?{}`, `partition?`, `count?`(≤1000), `compression?`(gzip/lz4/zstd/snappy) | `partition,offset,timestamp` |
| `kafka/messages/consume` | 见下 | `messages[]`,`scanned`,`matched`,`limited`,`hasMore`,`nextPartitionOffsets{}` |
| `kafka/messages/export` | 同 consume + `format`(json/csv), `limit`(≤10000) | `content`,`filename`,`contentType` |
| `kafka/stream/start` | 同 consume 参数 | `sessionId` |
| `kafka/stream/stop` | `sessionId`（或 `all:true`） | — |
| `kafka/stream/pause` / `kafka/stream/resume` | `sessionId` | `status` |
| `kafka/stream/status` | `sessionId` | `status{paused,totalScanned,totalMatched,bufferSize,partitionOffsets}` |
| `kafka/stream/messages` | `sessionId`, `offset`, `limit` | ring buffer 历史分页 |
| `kafka/presets/list|save|remove` | 消费/过滤预设 | 照 ldap/presets 形态（store 持久化） |
| `kafka/connections/statuses` | — | 照 ldap/connections/statuses 形态 |

### 5.3 consume 参数（一次性与流式共用 `ConsumeParams`）

`topic`、`groupId?`、`offsetStrategy`(latest/earliest/committed/timestamp/offset)、
`offsetTime?`（RFC3339 或 unix ms）、`partitions?[]`、`partitionOffsets?{partition:offset}`
（有 partitions 时禁 groupId；strategy=offset 时必填）、`limit`(默认100)、
`timeoutMs`(默认5000)、`maxScanRecords`(默认 max(1000, limit×10))、
`isolationLevel`(read_uncommitted/read_committed)、`commit`(true 时禁一切过滤且必须 groupId)、
过滤：`filter?`（全文=key+value+headers 拼接）、`keyFilter?`、`valueFilter?`、
`headerFilter?`、`matchMode`(contains/prefix/exact/regex)、
`fieldFilters?[]{source(value/key/header/topic/partition/offset/timestamp), path?, operator(contains/prefix/exact/regex/exists/not_exists/gt/gte/lt/lte), value, enabled}`,
`timestampFrom?/timestampTo?/offsetFrom?/offsetTo?`、
解码：`decode`(none/base64)、`decompression`(gzip/lz4/zstd/snappy)。

消息形状（二进制保真）：`{topic,partition,offset,timestamp,leaderEpoch?,key?/keyBase64?,valueText?,valueBase64?,headers{},committed?,decodeError?,truncated?}`
—— value 超过 8KB 只给 `valueBase64` + `truncated:true`？否：**valueText 恒为
UTF-8 安全预览（非法字节替换），valueBase64 恒完整**；消息体上限单条 512KB
（超出截断并标记）。

### 5.4 事件

| 事件 | 载荷 |
| --- | --- |
| `kafka/stream/messages` | `{sessionId, messages[], totalScanned, totalMatched, paused}`（200ms/批 50 节流） |
| `kafka/stream/error` | `{sessionId, error}` |
| `kafka/audit` | M0 审计记录（同 ldap/audit 形状） |

### 5.5 流式会话约束

ring buffer 10000 条；会话上限 20；空闲 30 分钟回收；`StopStream` 可取消；
fetch 错误指数退避 500ms→30s；**只读策略下禁止 commit**。

## 6. 安全策略（policy.go）

- `read_only=true`：produce/create/alter/reset/ACL 写一律 `-32000` 拒绝
  （错误码语义 `blocked`，与 ldap policy 一致）。
- `allow_delete=false`：topics/delete、groups/delete、acls/delete 额外拒绝；
  read_only 下 allow_delete 无效（两者与门）。
- topics/delete 要求请求携带 `confirmTopic`（与 topic 同名的确认字段，防误删）。
- 凭据（sasl_password、tls_client_key）不落日志、不进审计 result、不回显。
- 审计：全部写操作 + 拒绝事件 → store.AppendAudit + `kafka/audit` 事件。

## 7. 前端实施

目录同 ldap/frontend（App.vue 单页多面板 + components/ + lib/）：

- `lib/api.ts`：`callKafka<T>(method, params)` 注入 connectionId，导出 `kafkaApi`。
- `lib/kafkaModel.ts`（纯函数 + spec）：消息格式化/解码二次转换
  （Base64/GZip/Hex/JSON pretty/BitSet）、topic 业务排序（内部 topic 沉底）、
  lag 计算、CSV/JSON 导出序列化、Confluent properties 粘贴解析（填表单用）。
- `lib/i18n.ts` 七语 + `i18n.spec.ts` 七语完整性守卫（照 ldap）。
- `lib/appearance.ts`/`hostTheme.ts`：先照 ldap 抄，后续收敛 shared/frontend。
- 组件（Phase 1）：
  - `TopicTree.vue`（左栏：topic/分组/internal 标记 + 过滤框）
  - `MessagesPanel.vue`（一次性消费表单 + 消息表 + 详情抽屉 + 导出；
    消费表单覆盖 §5.3 全参数）
  - `StreamPanel.vue`（流式：start/stop/pause/resume + 实时表 + 自动滚动 +
    用户上滚暂停滚动 + droppedRows 提示，收 `kafka/stream/messages` 事件）
  - `ProducePanel.vue`（key/value/headers JSON 校验/partition/count/压缩）
  - `TopicsPanel.vue`（create/delete(confirmTopic)/扩分区/config 查看/编辑/offsets）
  - `GroupsPanel.vue`（组列表 + lag 徽章 + describe + offsets 表 + reset/delete）
  - `BrokersPanel.vue`（broker 列表 + config）
  - `AclsPanel.vue`（list/create/delete）
  - `ConnectionsPanel.vue` / `AuditFeedPanel.vue`（照 ldap 改）
- mock：`mockDbxHost.ts` 内存假桥必须镜像真实桥当前形状（binary 事件双形状，
  经 `shared/frontend/binaryEvent.ts` 归一化——本插件消息走 invoke 返回值，
  事件是 JSON 载荷，无二进制通道，但仍按规范引 shared）。
- 导出下载：Blob URL 兜底（宿主 1.0 无 save-file）。

## 8. 测试计划

- **单测**（纯解析，不连网）：client 构建/TLS+SASL 参数矩阵、consume 参数
  校验（commit×过滤互斥、partitions×groupId 互斥）、过滤引擎（matchMode、
  fieldFilters、JSON path、数值比较）、解码/解压、CSV/JSON 序列化、
  policy 门禁矩阵、manifest_contract_test（manifest 七语与方法表对齐）、
  前端 kafkaModel/i18n spec。
- **smoke**（`scripts/smoke_test.py`，sidecar_client_jsonl.py 直驱 sidecar）：
  S1 initialize+connection/test 无连接参数错误码正确；S2 假连接 connection/test
  返回业务错（非崩溃）；S3-S10 容器场景（KRaft）：connect → produce →
  consume(roundtrip) → topics/list 含种子 topic → groups/acls 容器不支持则
  SKIP → stream start/收事件/stop → export → policy read_only 拒绝写。
  SKIP 三层语义照 ldap：容器不可达整套 SKIP（`KAFKA_TEST_REQUIRE=1` 转
  FAIL）、sidecar 缺失 SKIP、未注册方法单场景 SKIP。
- **容器**：`docker-compose.kafka-test.yml`（apache/kafka KRaft 单机，
  container_name `dbx-kafka-test`，9092；无凭据字面量，探活发真实
  metadata 请求而非端口探测）；`smoke_container.py` 编排同 ldap 模式。
- **收口**：`scripts/test.sh` 全绿（SKIP 允许）才算完成定义达成。

## 9. 里程碑与并发分路

三路 agent 并行（契约以本文件为准，接口冻结）：

| 路 | 范围 | 交付 |
| --- | --- | --- |
| A backend | `kafka/backend/**` + `docs/PROGRESS-B-KAFKA.zh-CN.md` | go vet/test 过；方法全注册；单测齐 |
| B frontend | `kafka/frontend/**` + `docs/PROGRESS-P-KAFKA.zh-CN.md` | typecheck/test/build 过；七语齐 |
| C scaffold | manifest/dbx-plugin.toml/README/.gitignore/assets/scripts/docker-compose/docs(PROTOCOL) + 根 README/AGENTS 修订 | scripts 可跑；协议文档完整 |

收口（主线）：build.sh → test.sh → 修复 → 完成定义四件套核验。

## 10. 风险与备注

- franz-go 依赖需公网拉取进 go.sum；SDK replace 本地路径不影响打包
  （CLI 用 DBX_PLUGIN_SDK_ROOT+go.work 覆盖）。
- 宿主 bridge binary 事件契约与本插件无关（无二进制通道），但前端仍统一走
  `shared/frontend/binaryEvent.ts` 消费任何事件载荷字节（防未来演进）。
- Go 1.24 工具链与 CLI 捆绑 go.work 1.22 冲突：打包走原生 CLI 二进制
  （照 ldap/build.sh 的 PATH 兜底方案）。
- 大消息（512KB 上限）与流式背压：ring buffer 固定容量，前端 droppedRows
  计数提示，避免 OOM。
- tinyrdm 的 SR/Glue/Kerberos/Connector 导入不阻塞 Phase 1 验收，
  全部登记 Phase 2。

## 11. Phase 2 商用化落地记录（2026-09-05）

三路并发（D=backend+manifest+PROTOCOL、E=frontend+ag-grid、主线收口联调），
契约冻结后并行，无目录交叉。

### 11.1 交付

- **后端 45 方法**（Phase 1 的 34 + schema 族 11）：Confluent 兼容 SR REST
  （wire format 编解码、LCS diff、兼容性 get/set/check、注册/删除、
  per-consume 元数据缓存）、Kerberos/GSSAPI（gokrb5 + franz-go sasl/kerberos，
  keytab 只收路径）、ZooKeeper 发现（go-zookeeper/zk，chroot）、produce/consume
  的 `valueBase64/keyBase64/schema{}` 挂载、offsets 五策略
  （earliest/latest/max-timestamp/log-start/时间戳）。
- **manifest 新字段 10 个**（connection_source/zk_servers/kerberos 五件套/
  sr_url/sr_username/sr_password，sr_password 走 secret binding），
  sasl_mechanism 增加 GSSAPI，七语逐字段全量。
- **前端**：引入 `ag-grid-community`（36.1.0，MIT；理由：商用检索要求列内
  过滤，用户点名 table 组件）——消息/组/offsets/ACL/topics 六表 ag-grid 化
  （排序+列过滤+分页持久化+窄容器降级，`DbxAgGrid.vue` 统一封装对齐 DBX
  主题令牌）；新增 `SchemasPanel`（版本 diff/兼容性/注册/删除双确认）与
  `MonitorPanel`（lag 采样+趋势 SVG+阈值告警+方案 presets）；Confluent
  properties 导入助手（密码掩码不落盘）；七语 440 键/语。
- **测试**：backend 单测 74→98，前端 38→49；docker 双容器覆盖
  （apache/kafka KRaft + Redpanda SR）smoke S1-S11。

### 11.2 联调修复（收口主线）

- **reset `partitionOffsets` 形状三方不一致**（Phase 1 遗留）：backend 嵌套
  `map<topic,map<partition,int>>`、前端/PROTOCOL 扁平 → 以嵌套为准，
  前端新增 `parseGroupOffsetTargetsText`（`topic:0=100` 显式语法，单 topic
  可裸写 `0=100`，歧义报错）、api.ts 类型、PROTOCOL §3.3、七语 hint 同步。
- **打包失败**：全局 npm CLI wrapper 注入的捆绑 go.work 锁 go 1.22 与模块
  1.24 冲突 → `test.sh` package 段照 `build.sh` 直调原生 CLI 二进制
  （`env -u DBX_PLUGIN_SDK_ROOT`）。
- 容器真跑暴露并已修（D 路，详 PROGRESS-B §6.3）：无 group 消费建 client
  失败、流式 poll 无限期阻塞导致事件不出、ACL 空 operation 误报、
  sidecar_client readline 无超时挂死、Redpanda DELETE version 单数字兼容。

### 11.3 验证证据

- `bash scripts/build.sh` → `dist/io.dbx.kafka-0.1.0-darwin-arm64.dbxp`。
- `bash scripts/test.sh`（含 package 步骤修复后复跑）→ 前端三件套 +
  go vet/test + package + smoke `total=11 PASS=10 FAIL=0 SKIP=1`
  （S3 合法 SKIP：新集群无消费组提交，`__consumer_offsets` 未建）。
- ui_test.mjs（Playwright+mock 桥）：ag-grid 过滤/分页持久化、Schemas diff、
  Monitor 采样/阈值/方案、`?ro=1` 只读禁用、暗色主题可读。

### 11.4 完成定义四件套核验

| 项 | 状态 |
| --- | --- |
| 单测（纯解析不连远端） | go 3 包 ok（98 例）+ vitest 6 文件 49 例 |
| smoke（未实现方法 SKIP 不 FAIL） | S1-S11，SKIP 语义三层齐 |
| 对标/任务清单更新 | 本文件 §11 + PROGRESS-B/-P 各 Phase 2 小节 |
| 七语文案 | manifest 逐字段 × 前端 440 键 × 7 语，spec 守卫绿 |

### 11.5 AWS Glue Schema Registry 落地（2026-09-05 第三轮，补齐 tinyrdm 字面缺口）

- **后端**：`internal/kafkaconn/glue.go`（aws-sdk-go-v2/service/glue，auth_mode
  default/static；API 映射照 tinyrdm：ListSchemas/ListSchemaVersions/
  GetSchemaVersion/RegisterSchemaVersion/CreateRegistry/UpdateSchema
  compatibility/CheckSchemaVersionValidity/DeleteSchema*），11 个 schema 方法
  改 backend 分发，请求 `registry?: "confluent"|"glue"`（缺省自动探测，歧义
  -32602）；compatibility 枚举含 Glue 8 档；**消息编解码挂载在 glue 下返回
  tinyrdm 同款业务错**（tinyrdm 原文语义：schema-aware 编解码仅 Confluent
  wire format，Glue 仅管理面）。
- **manifest**：glue_region/glue_registry_name/glue_auth_mode/glue_access_key_id/
  glue_secret_access_key(secret)/glue_session_token(secret)，七语全量。
- **前端**：SchemasPanel registry 徽章+双后端切换（枚举 7/8 档联动）、
  ConnectionsPanel Glue 摘要（secret 只显已配置态）、三面板 schema 挂载在
  glue 下禁用+提示、`?glue=1` mock 模式、i18n 454 键/语。
- **消息二次解码补齐**：fzstd/snappyjs/lz4js（MIT/ISC 纯 JS）落地，详情侧
  gzip/zstd/snappy/lz4 四解压全支持（tinyrdm ConvertValue 对齐，Phase 2 降级
  标注撤销）。
- **验证**：backend 105 例（httptest 假 Glue JSON-RPC： subjects/versions/get/
  register/compat/delete/错误透传/探测歧义/枚举全表）；前端 52 例（zstd CLI
  固定向量 + snappy/lz4 roundtrip + 失败降级）；smoke S12（Glue 需
  GLUE_TEST_REGION/GLUE_TEST_REGISTRY，本地无 Glue 容器即 SKIP）；收口
  test.sh 全绿。
- 遗留：真实 AWS Glue 往返需凭据环境跑 S12；Glue DISABLED 与 Confluent NONE
  语义差异文档化；proto 载荷/幂等生产参数仍 Phase 3。
