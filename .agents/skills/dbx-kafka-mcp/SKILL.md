---
name: dbx-kafka-mcp
description: 通过 MCP 快速调用 DBX Kafka 工具（mcp__dbx-kafka__*）完成任务：消费/检索消息、生产测试消息、投递延迟与内容分析、topic 与消费组运维。当用户要求看某 topic 的消息、发消息、统计消息内容、清理或重置 topic/消费组，或提到 kafka_messages_digest/kafka_messages_produce 等工具时使用。
---

# Kafka MCP 快速调用（mcp__dbx-kafka__*）

工具定位口诀：**看消息 digest，发消息 produce，运维三件套全两阶段；写必
`readOnly:false`，删除叠加 `allowDelete:true`**。

## 工具地图（11 个）

| 意图 | 工具 | 要点 |
| --- | --- | --- |
| 消费/检索消息 | `kafka_messages_digest` | `offsetStrategy` 默认 earliest；`filter` 全文（key+value+headers）、`keyFilter`/`valueFilter`、`matchMode`（contains/prefix/exact/regex） |
| 按字段分析 | `kafka_messages_digest` `fields:["$.user.id"]` / `fieldFilters` | JSON-path 投影出 distinct/topN；过滤同理按 value/key/header 通道 |
| 精确翻页 | `kafka_messages_digest` `format:"rows"` → `kafka_cursor_next` | 每页 n≤20，行是 topic-partition-offset 定位符 |
| 发一条消息 | `kafka_messages_produce` | 单阶段直执行；消息体 ≤64 KiB；`headers` 字符串 map |
| 删 topic | `kafka_topics_delete` | 两阶段 |
| 清空 topic 消息 | `kafka_topics_records_clear` | 两阶段（truncate 到高水位） |
| 重置消费组位点 | `kafka_groups_offsets_reset` | 两阶段；`resetTo` earliest/latest/timestamp/partitionOffset，各模式配套参数不同 |
| 找合法 topic 名 | `kafka_ui_topics` | **stdio 恒 UNAVAILABLE**——stdio 下没有 topic 列表能力，名称从用户/集群文档获取 |

## 连接参数（内联必填项）

`brokers` 必填（数组或逗号/换行分隔串，如 `["127.0.0.1:9092"]`）。可选：
`securityProtocol`+`saslMechanism`+`saslUsername`/`saslPassword`（SASL）、
`tlsCaCert`/`tlsClientCert`/`tlsClientKey`（TLS）、`schemaRegistry`+URL+账号
（Confluent SR 解码 wire format 后再投影）。相同参数池化为 `mcp-<hash>`，
后续调用可只传回池化 `connectionId`。

**标准顺序**：① 内联 `brokers` 直连（仅限本机可信会话）② DBX 保存连接 →
传 `connectionId`（应用运行中自动桥转发，凭据不出参数）。

## 写操作门（最容易卡住的一步）

内联连接**默认 `readOnly:true`**。写被拒时按拒绝消息补参数重发：

| 操作 | 必带 |
| --- | --- |
| produce / offsets_reset | `readOnly: false` |
| topics_delete / records_clear | `readOnly: false` + `allowDelete: true` |

## 硬限制

| 限制 | 值 | 超限动作 |
| --- | --- | --- |
| 单消息 | 64 KiB | 走工作台/业务通道 |
| 扫描预算 | `maxScanRecords` 默认 1000（上限 10 万） | 收窄 partition/时间窗/加 filter |
| 单响应 | 16 KiB（`truncated:true`） | rows+cursor、减 fields/样本 |
| cursor | n≤20，TTL 10 min | 过期重新 digest |
| confirmToken | 60 s，参数一变即作废 | 重新 preview |

## 高频坑

- **`scanned:0` ≠ 没数据**：可能是 topic 名错（sidecar 会补存在性校验报错）或
  位置策略错过——`offsetStrategy` 显式给 `earliest` 从头扫。
- **`scanTruncated:true`** 表示达到扫描预算提前收手：统计只是子集结论。
- 两阶段 preview 消耗一次往返：改任何参数（连 `topics` 顺序无关的值变化）都会
  使 token 作废，先想清楚再 preview。
- `decode:"base64"` 只做值的二次解码；压缩消息通常自动解压，异常时才需手动
  指定 `decompression`。
- 消费组重置前先确认组无活跃成员，否则 broker 侧可能拒绝。

更多背景（配置、桥接兜底、契约细节）见 skill `dbx-mcp-usage` 与
`shared/TEST_PLAN_MCP_ZCODE.zh-CN.md`。
