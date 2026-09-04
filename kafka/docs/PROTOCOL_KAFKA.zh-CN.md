# DBX Kafka 插件协议文档（PROTOCOL_KAFKA）

> 版本基准：kafka v0.1.0（Phase 1）。素材来源与唯一权威：
> `docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md` §5（实现必须与本文件同步；
> 按 AGENTS.md 规则 2，**新增/变更方法必须同步本文档**）。

## 1. 公共约定

- **传输**：stdio-jsonl —— SDK 帧协议，stdin/stdout 每行一条 JSON-RPC 2.0
  消息（`\n` 结尾），stderr 为诊断通道。
- **方法命名**：`<域>/<动作>`（如 `kafka/topics/list`）。
- **字段命名**：camelCase。
- **connectionId 必填**：除生命周期三方法与事件外，所有领域方法的
  `params.connectionId` 必填；宿主按 workbench 上下文注入。缺失或未知
  连接 → `-32602` / 业务错。
- **错误码**：
  | 码 | 语义 | 例 |
  | --- | --- | --- |
  | `-32602` | 参数错（缺字段/类型不符/取值非法） | `connectionId` 缺失、`partitions` 非法、consume 参数互斥冲突 |
  | `-32000` | 业务错（PluginError） | 连接失败、broker 拒绝、**策略拒绝（blocked）**、未知连接 |
  | `-32601` | 方法未注册/未实现 | 并行开发期，smoke 对其 SKIP 而非 FAIL |
- **响应形状**：成功统一 `{ "ok": true, "data": <payload> }`；失败走
  PluginError（JSON-RPC `error`，`message` + `code`，`data` 附加上下文）。
  下文各方法只描述 `data` 载荷。
- **凭据红线**：`sasl_password`、`tls_client_key` 等 secret binding 字段
  不落日志、不进审计 result、不回显。

## 2. 生命周期方法（SDK/ldap 同款）

| 方法 | 请求 | 返回（data） | 错误语义 |
| --- | --- | --- | --- |
| `connection/test` | `provider{id,databaseType:"kafka"}`、`connection{id,name,external_config,connection_secrets}`、`runtime{host,port}` | `{success:true}` | 拨号/认证失败 → `-32000`；连接参数缺失/非法 → `-32602` |
| `connection/connect` | 同上 | `{success:true}` | 同上；成功后按 `connectionId` 缓存 client（指纹失效重建） |
| `connection/disconnect` | `connection{id}` | `{success:true}` | 释放缓存 client 与流式会话 |

配置字段来源：`external_config` 为 manifest config binding 字段
（bootstrap_servers、security_protocol、sasl_*、tls_*、client_id、
read_only、allow_delete），`connection_secrets` 为 secret binding 字段
（sasl_password、tls_client_key）。

## 3. 领域方法

### 3.1 brokers

**`kafka/brokers/list`**

- 请求：无附加字段（`connectionId` 必填）。
- 返回：`brokers[]{nodeId:int, host:string, port:int, rack:string?}`。
- 错误：连接不可用 → `-32000`。

**`kafka/brokers/config`**

- 请求：`brokerId:int`（必填）。
- 返回：`entries[]{name, value:string, source, sensitive:bool, isDefault:bool}`；
  `sensitive=true` 的条目 value 以掩码返回。
- 错误：`brokerId` 缺失 → `-32602`；broker 不存在 → `-32000`。

### 3.2 topics

**`kafka/topics/list`**

- 请求：`includeInternal?:bool`（默认 false，隐藏 `__consumer_offsets` 等内部 topic）。
- 返回：`topics[]{name, topicId:string?, isInternal:bool, partitionCount:int, replicationFactor:int, error?:string}`。
- 错误：连接不可用 → `-32000`。

**`kafka/topics/describe`**

- 请求：`topic:string`（必填）。
- 返回：`partitions[]{partition:int, leader:int, leaderEpoch:int?, replicas:int[], isr:int[], offlineReplicas:int[], isHealthy:bool}`
  （isHealthy = ISR 覆盖 replicas 且无 offline）。
- 错误：`topic` 缺失 → `-32602`；topic 不存在 → `-32000`。

**`kafka/topics/create`**

- 请求：`topics:string[]`（≥1）、`partitions:int`（≥1）、
  `replicationFactor:int`（≥1，受集群 broker 数上限）、
  `config?:map<string,string>`（逐 topic 应用）。
- 返回：`results[]{topic:string, ok:bool, error?:string}`（逐条成败）。
- 错误：read_only → `-32000`（blocked）；参数非法 → `-32602`；审计记一条。

**`kafka/topics/delete`**（critical 门禁）

- 请求：`topics:string[]`、`confirmTopic:string`（必填，与被删 topic 同名的
  确认字段，防误删；任一条不匹配 → `-32602`）。
- 返回：`results[]{topic, ok, error?}`。
- 错误：read_only 或 `allow_delete=false` → `-32000`（blocked）；审计。

**`kafka/topics/partitions/update`**

- 请求：`partitions:map<topic,int>`（新分区数，**只增**——小于当前值 → `-32602`）。
- 返回：`results[]{topic, ok, error?}`。
- 错误：read_only → `-32000`（blocked）；审计。

**`kafka/topics/config/get`**

- 请求：`topic:string`。
- 返回：`entries[]`（同 brokers/config 形状）。

**`kafka/topics/config/alter`**

- 请求：`topic:string`、`config:map<string,string>`（set）、
  `deleteKeys?:string[]`（还原默认）。
- 返回：`entries[]`（alter 后的当前配置）。
- 错误：read_only → `-32000`（blocked）；审计。

**`kafka/topics/offsets/list`**

- 请求：`topics:string[]`、`offsetTime?:string`（`earliest` | `latest` |
  RFC3339 | unix 毫秒；默认 `latest`）。
- 返回：`rows[]{topic, partition:int, offset:int, timestamp:int?, leaderEpoch:int?}`。
- 错误：时间解析失败 → `-32602`。

### 3.3 groups

**`kafka/groups/list`**

- 请求：无附加字段。
- 返回：`groups[]{group:string, state:string, protocolType:string, coordinator:int?}`。

**`kafka/groups/describe`**

- 请求：`group:string`。
- 返回：`members[]{memberId, instanceId?, clientId, clientHost, assignments:map<topic,int[]>}`。
- 错误：组不存在 → `-32000`。

**`kafka/groups/offsets/list`**

- 请求：`group:string`、`topics?:string[]`（空/缺省 = committed 全量）。
- 返回：`rows[]{topic, partition:int, startOffset:int, endOffset:int, committedOffset:int, lag:int}`、
  `totalLag:int`、`hasCommitted:bool`。
  **Option 语义**：组从未提交（`__consumer_offsets` 无记录）→
  `hasCommitted:false` 且 `rows` 为空，与"已提交且零 lag"明确区分。
- 错误：组不存在 → `-32000`。

**`kafka/groups/delete`**（critical 门禁）

- 请求：`group:string`。
- 返回：空 `data`。
- 错误：read_only 或 `allow_delete=false` → `-32000`（blocked）；审计。

**`kafka/groups/offsets/reset`**

- 请求：`group:string`、`topics:string[]`、
  `resetTo:"earliest"|"latest"|"timestamp"|"partitionOffset"`、
  `timestampMs?:int`（resetTo=timestamp 必填）、
  `partitionOffsets?:map<partition,int>`（resetTo=partitionOffset 必填）。
- 返回：`rows[]{topic, partition:int, ok:bool, error?}`。
- 错误：read_only → `-32000`（blocked）；组合参数缺失/冲突 → `-32602`；
  审计。

### 3.4 acls

**`kafka/acls/list`**

- 请求：`filter{}`（`resourceType?:topic|group|cluster|transactionalId|delegationToken|user`、
  `resourceName?`、`patternType?`、`principal?`、`host?`、`operation?`、
  `permissionType?`）。**过宽过滤拒绝**：filter 为空对象 → `-32602`
  （防全量枚举打爆 controller）。
- 返回：`acls[]{resourceType, resourceName, patternType, principal, host, operation, permission}`。

**`kafka/acls/create`**

- 请求：`acl{resourceType, resourceName, patternType, principal, host, operation, permission}`（全必填）。
- 返回：空 `data`。
- 错误：read_only → `-32000`（blocked）；字段非法 → `-32602`；审计。

**`kafka/acls/delete`**（critical 门禁）

- 请求：`filter{}`（同 list 的形状，同样拒绝过宽）。
- 返回：`matched[]{...同 acl 条目}`（删除前匹配到的条目）。
- 错误：read_only 或 `allow_delete=false` → `-32000`（blocked）；审计。

### 3.5 messages

**`kafka/messages/produce`**

- 请求：`topic:string`、`key?:string`、`value:string`（必填）、
  `headers?:map<string,string>`、`partition?:int`、
  `count?:int`（批量条数，≤1000，默认 1）、
  `compression?:"gzip"|"lz4"|"zstd"|"snappy"`。
- 返回：`{partition:int, offset:int, timestamp:int}`（首条消息定位；
  count>1 时为末条 offset）。
- 错误：read_only → `-32000`（blocked）；count 超限 → `-32602`；
  topic 不存在且未自动创建 → `-32000`；审计。

**`kafka/messages/consume`**

- 请求：`ConsumeParams`（见 §4，`topic` 必填）。
- 返回：`{messages:MessageView[], scanned:int, matched:int, limited:bool, hasMore:bool, nextPartitionOffsets:map<partition,int>}`
  （`nextPartitionOffsets` 可作续读游标）。
- 错误：参数互斥冲突（见 §4）→ `-32602`；连接不可用 → `-32000`。

**`kafka/messages/export`**

- 请求：`ConsumeParams` + `format:"json"|"csv"`、`limit?:int`（≤10000，默认 1000）。
- 返回：`{content:string, filename:string, contentType:string}`；
  filename 形如 `dbx-kafka-<topic>-<yyyyMMdd-HHmmss>.json|.csv`。
  宿主 1.0 无 save-file 能力，前端以 Blob URL 下载。
- 错误：format 非法 / limit 超限 → `-32602`。

### 3.6 stream（流式消费会话）

**`kafka/stream/start`**

- 请求：`ConsumeParams`（同一次性消费）。
- 返回：`{sessionId:string}`。
- 会话上限 20，超出 → `-32000`；参数互斥冲突 → `-32602`。

**`kafka/stream/stop`**

- 请求：`sessionId?:string`（与 `all:true` 二选一；都没有 → `-32602`）。
- 返回：空 `data`。

**`kafka/stream/pause` / `kafka/stream/resume`**

- 请求：`sessionId:string`。
- 返回：`{status:string}`（`"paused"` / `"running"`）。
- 错误：未知 sessionId → `-32000`。

**`kafka/stream/status`**

- 请求：`sessionId:string`。
- 返回：`{paused:bool, totalScanned:int, totalMatched:int, bufferSize:int, partitionOffsets:map<partition,int>}`。

**`kafka/stream/messages`**（方法，非事件）

- 请求：`sessionId:string`、`offset:int`、`limit:int`（ring buffer 历史分页）。
- 返回：`{messages:MessageView[], total:int, offset:int}`。

### 3.7 presets 与连接状态（照 ldap/presets、ldap/connections/statuses 形态）

**`kafka/presets/list`** — 无附加字段；返回 store 持久化的消费/过滤预设列表。

**`kafka/presets/save`** — `preset{id?, name, payload}`；返回 `{id}`。新增落 store。

**`kafka/presets/remove`** — `id:string`；返回空 `data`；未知 id → `-32000`。

**`kafka/connections/statuses`** — 无附加字段；返回
`statuses[]{connectionId, connected:bool, brokers:int, streams:int}` 形态。

### 3.8 流式会话约束（IMPL_PLAN §5.5）

- ring buffer 固定容量 **10000** 条（事件 + `kafka/stream/messages` 分页共用）；
- 并发会话上限 **20**；
- 空闲 **30 分钟**自动回收；
- `kafka/stream/stop` 可取消进行中的 fetch；
- fetch 错误指数退避 **500ms → 30s**；
- **read_only 策略下禁止 commit**（`ConsumeParams.commit=true` → `-32000`）。

## 4. ConsumeParams 完整字段表

一次性消费（`kafka/messages/consume`、`kafka/messages/export`）与流式
（`kafka/stream/start`）共用：

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `topic` | string | 必填 | 目标 topic |
| `groupId` | string? | — | 消费组 id；**与 `partitions` 互斥**（同给 → `-32602`） |
| `offsetStrategy` | enum | `latest` | `latest` / `earliest` / `committed` / `timestamp` / `offset` |
| `offsetTime` | string? | — | strategy=timestamp：RFC3339 或 unix 毫秒 |
| `partitions` | int[]? | — | 指定分区（有值时禁 `groupId`） |
| `partitionOffsets` | map<partition,int>? | — | strategy=offset 时**必填** |
| `limit` | int | 100 | 返回条数上限 |
| `timeoutMs` | int | 5000 | 单次 fetch 等待 |
| `maxScanRecords` | int | max(1000, limit×10) | 扫描上限（过滤不过 early-stop） |
| `isolationLevel` | enum | `read_uncommitted` | `read_uncommitted` / `read_committed` |
| `commit` | bool | false | true 时**禁一切过滤且必须 groupId**（否则 → `-32602`）；read_only 下拒绝 → `-32000` |
| `filter` | string? | — | 全文过滤（key+value+headers 拼接） |
| `keyFilter` | string? | — | key 过滤 |
| `valueFilter` | string? | — | value 过滤 |
| `headerFilter` | string? | — | headers 序列化后过滤 |
| `matchMode` | enum | `contains` | `contains` / `prefix` / `exact` / `regex`（非法 regex → `-32602`） |
| `fieldFilters` | FieldFilter[]? | — | 字段级过滤，见下 |
| `timestampFrom` / `timestampTo` | int? | — | 消息时间戳范围（unix 毫秒，闭区间） |
| `offsetFrom` / `offsetTo` | int? | — | offset 范围 |
| `decode` | enum | `none` | `none` / `base64`（对 valueText 的二次解码展示） |
| `decompression` | enum | — | `gzip` / `lz4` / `zstd` / `snappy`（payload 先解压再解码） |

`fieldFilters[]`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `source` | enum | `value` / `key` / `header` / `topic` / `partition` / `offset` / `timestamp` |
| `path` | string? | JSON path（source=value/key 且 payload 为 JSON 时按路径取值） |
| `operator` | enum | `contains` / `prefix` / `exact` / `regex` / `exists` / `not_exists` / `gt` / `gte` / `lt` / `lte` |
| `value` | string? | 比较值（exists/not_exists 可省；数值比较按数值解析） |
| `enabled` | bool | 默认 true；false 的条目跳过 |

`commit=true` 与任何过滤条件（filter/keyFilter/valueFilter/headerFilter/
fieldFilters/时间戳或 offset 范围）同给 → `-32602`（commit 与过滤互斥）。

## 5. 消息形状（MessageView，二进制保真）

```
{
  "topic": string,
  "partition": int,
  "offset": int,
  "timestamp": int,
  "leaderEpoch": int?,
  "key": string?,          // UTF-8 安全预览（非法字节替换 U+FFFD）
  "keyBase64": string?,    // 恒完整
  "valueText": string?,    // UTF-8 安全预览（非法字节替换），恒有
  "valueBase64": string,   // 恒完整（base64 保真，二进制不损坏）
  "headers": map<string,string>,
  "committed": bool?,
  "decodeError": string?,  // 二次解码/解压失败原因
  "truncated": bool        // 单条消息体超 512KB 时截断并标记
}
```

- 单条消息体上限 **512KB**，超出截断且 `truncated:true`；
- `valueText` 恒为 UTF-8 安全预览、`valueBase64` 恒完整（tinyrdm
  `string(record.Value)` 二进制损坏问题的修正）。

## 6. 事件（sidecar → 宿主 notification）

### 6.1 `kafka/stream/messages`

流式消费批量推送（节流：200ms 或 50 条一批）：

```
{ "sessionId": string, "messages": MessageView[], "totalScanned": int,
  "totalMatched": int, "paused": bool }
```

### 6.2 `kafka/stream/error`

```
{ "sessionId": string, "error": string }
```

fetch 循环按指数退避（500ms→30s）重试；不可恢复错误（topic 删除、
会话被回收）随后由宿主主动 stop。

### 6.3 `kafka/audit`

M0 审计记录（同 ldap/audit 形状）：全部写操作 + 策略拒绝事件，
同时落 `store.AppendAudit`（audit.jsonl）并推送本事件；凭据字段
（sasl_password、tls_client_key）不进 result。

## 7. 策略语义（policy.go，错误均 `-32000` blocked）

| 配置 | 效果 |
| --- | --- |
| `read_only=true` | produce / topics create/alter/delete / partitions update / groups delete / offsets reset / acls create/delete 一律拒绝；**禁止 commit** |
| `allow_delete=false` | topics/delete、groups/delete、acls/delete 额外拒绝；read_only 下本项无效（两者与门） |
| `confirmTopic` | topics/delete 必须携带与 topic 同名的确认字段，否则 `-32602` |

## 8. 与实现的同步约定

- 后端方法注册表与本文件及 `IMPL_PLAN_DBX_KAFKA.zh-CN.md` §5.2 三方一致；
  `backend/internal/kafkaconn/manifest_contract_test.go` 会读取
  `manifest.json` 做七语与字段契约校验。
- smoke（`scripts/smoke_test.py`）按本文件场景编号 S1-S10；
  未注册方法（`-32601`）单场景 SKIP。
