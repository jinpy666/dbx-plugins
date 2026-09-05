---
name: dbx-kafka-dev
description: DBX Kafka 插件（io.dbx.kafka，Go sidecar + franz-go）的开发入口：实施文档、协议形态、脚本与测试集群。当用户在 kafka/ 目录下开发、调试 topic/消息生产消费/消费组/ACL/Schema Registry 能力、跑 kafka smoke 或排查 Go sidecar / stdio-jsonl 协议问题时使用。
---

# DBX Kafka 插件（io.dbx.kafka）开发入口

Go sidecar（官方 Go SDK，`stdio-jsonl`），二进制 `dbx-plugin-kafka`；Kafka
客户端 `twmb/franz-go` + `pkg/kadm`（CGO=0）。能力：topic/分区浏览与健康
视图、消息生产与消费（5 种 offset 策略、字段级过滤、base64 + 四种解压解码）、
流式消费会话（ring buffer、暂停/恢复）、消费组 lag 快照与 offset 重置、
ACL 管理、JSON/CSV 导出、Schema Registry（Confluent 兼容含 Redpanda +
AWS Glue，编解码仅 Confluent wire format）、Kerberos/GSSAPI、ZooKeeper 发现。

- **唯一工作来源**：`kafka/docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md`（Phase 1/2 已
  收口；Phase 3 登记 OAUTHBEARER、PROTOBUF 载荷、幂等/事务生产参数，
  见 §0.2 / §11.5）；协议契约为 `kafka/docs/PROTOCOL_KAFKA.zh-CN.md`；
  进度记 `kafka/docs/PROGRESS-*.zh-CN.md`。
- 通用规范（环境/硬性约定/宿主契约监察/故障速查）：skill `dbx-plugin-dev`；
  宿主验证：skill `dbx-host-e2e`。
- 连接凭据走宿主 secret binding，插件不持久化；只读连接写操作拒绝，
  删除类操作过确认门。

## 脚本与测试

```bash
scripts/build.sh                 # 前端三件套(typecheck/test/build) + go build + dbx-plugin package
scripts/test.sh                  # 全套：前端三件套 → go vet/test → 打包 → smoke（all green 才算过）
scripts/smoke_test.py            # S1-S12 无 UI 全链路冒烟（无 Kafka 容器时自动 SKIP）
scripts/smoke_container.py       # 测试容器编排入口（apache/kafka KRaft + redpanda Schema Registry）
scripts/dev-cluster.sh up|status|seed|down   # 本地可连调集群：PLAINTEXT 127.0.0.1:9092 + SR 19081，
                                             # 种子 topic 幂等重建，起完即可在宿主新建连接实测
scripts/sidecar_client_jsonl.py  # stdio-jsonl 协议客户端（直接驱动 sidecar 调试）
scripts/ui_test.mjs              # 前端 UI 测试
```

- 种子数据：`scripts/kafka-seed/`（dev-cluster seed 与 smoke 共用）。
- 未实现/环境不支持的方法在 smoke 中必须 SKIP 而非 FAIL（如 PLAINTEXT
  容器段 ACL 不支持即合法 SKIP）。

## 协议要点

- stdio-jsonl：每行一个 JSON 消息（与 ssh/files 的 5 字节帧头不同），
  调试一律用 `sidecar_client_jsonl.py`，勿手搓。
- 消息二进制保真：key/value/headers base64 编解码，gzip/snappy/lz4/zstd
  解压；消费语义与过滤引擎迁移自 tiny-rdm。
- 容器就绪判定用真实 metadata 请求（`kafka-topics.sh --list`），不做裸端口探测。
