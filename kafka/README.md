# dbx-kafka-plugin（io.dbx.kafka）

DBX 的 Kafka 控制台插件：topic/分区浏览与健康视图、消息生产与消费
（5 种 offset 策略、字段级过滤、base64/四种解压解码）、流式消费会话
（ring buffer、暂停/恢复）、消费组 lag 快照与 offset 重置、ACL 管理、
JSON/CSV 导出。能力重写自 tiny-rdm 的 Kafka 实现（取其消费语义与过滤
引擎，补其二进制保真与连接复用短板）。2026-09-05 经用户决策新增
（工作区原"不做新插件"约束随之解除）。

## 状态

**商用级可用**（2026-09-05 单日完成 Phase 1 + Phase 2，三路 agent 并行 +
主线收口）：45 个 sidecar 方法、Schema Registry（Confluent 兼容含 Redpanda）、
Kerberos/GSSAPI、ZooKeeper 发现、ag-grid 表格过滤检索、Schemas/Monitor 面板、
七语 440 键。验证：backend 单测 98 例、前端 49 例、docker 双容器 smoke
S1-S11（10 PASS / 1 合法 SKIP）、`.dbxp` 打包成功。AWS Glue SR 管理面同日第三轮落地
（tinyrdm 双 SR 后端全覆盖；消息编解码按 tinyrdm 口径仅 Confluent wire format）。
Phase 3 登记：OAUTHBEARER、PROTOBUF 载荷、幂等/事务生产参数
（见 IMPL_PLAN §0.2 / §11.5）。

## 技术形态

- sidecar：**Go**（官方 Go SDK，`stdio-jsonl`），二进制 `dbx-plugin-kafka`
- Kafka 客户端：`github.com/twmb/franz-go` + `pkg/kadm`（tinyrdm 同款，
  迁移成本最低）；`CGO_ENABLED=0`
- 连接：宿主 connection-provider（`database_type: "kafka"`），认证
  PLAINTEXT / SSL / SASL_PLAINTEXT / SASL_SSL（PLAIN、SCRAM-256/512），
  凭据（sasl_password、tls_client_key）走宿主 secret binding，插件不持久化
- 传输：SSH 隧道/代理由宿主承担，sidecar 经 `runtime.host:port` 拨号；
  客户端按 connectionId 缓存复用
- 安全：宿主 read_only + allow_delete 策略 + topics/delete confirmTopic
  确认门禁 + 全写操作审计

## 文档

- 实施文档（唯一工作来源）：[docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md](docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md)
  ——三方对标、manifest 字段全表、sidecar 方法契约、安全策略、前端组件、
  smoke 场景 S1–S10、并行分路
- 协议文档（新方法必须同步）：
  [docs/PROTOCOL_KAFKA.zh-CN.md](docs/PROTOCOL_KAFKA.zh-CN.md)
- 公共基线：`../shared/IMPL_PLAN_M0_COMMON.zh-CN.md`（lifecycle/审计/测试基建）

## 快速开始

```bash
# 1. 构建前端 + 打包 .dbxp（后端在位时一并 go build）
bash scripts/build.sh

# 2. 全量验证：前端三件套 → UI 走查 → go vet/test → 打包 → smoke
bash scripts/test.sh

# 3. 容器 smoke（apache/kafka KRaft 单机，自动建容器/种子 topic/回收）
python3 scripts/smoke_container.py          # 加 --keep 保留容器调试

# 无容器时单独跑 smoke（S3+ 全套 SKIP，S1/S2 照常）
python3 scripts/smoke_test.py
```

## 本地快速连接（可测试环境）

用一个本地 PLAINTEXT 集群立刻测试插件连接（`scripts/dev-cluster.sh`，
包装 `docker-compose.kafka-test.yml`）：

```bash
# 1. 起集群（等真实 metadata 就绪 + 幂等建种子 topic + 打印连接参数，
#    容器保持运行）
bash scripts/dev-cluster.sh up

# 2. 宿主连接表单填：Bootstrap servers = 127.0.0.1:9092，
#    Security protocol = PLAINTEXT（无 SASL/TLS，无凭据）。
#    sidecar 经宿主拨号，宿主在本机故直连可用，无需隧道/代理。

# 3. 测完回收（容器 + 卷）
bash scripts/dev-cluster.sh down
```

`scripts/dev-cluster.sh status` 查看容器/端口状态与真实 metadata 探活；
`seed` 幂等重建种子 topic。种子 topic：`dbx-smoke-events`、
`dbx-smoke-binary`、`dbx-smoke-filter`、`dbx-smoke-stream`、
`dbx-smoke-export`。可选 Schema Registry（smoke S11）：`http://127.0.0.1:19081`。

## 环境变量

`DBX_PLUGIN_SIDECAR`（指定 sidecar 二进制，默认
`backend/bin/dbx-plugin-kafka`）、`KAFKA_TEST_HOST/PORT`（默认
127.0.0.1:9092）、`KAFKA_TEST_REQUIRE=1`（CI 中把环境类 SKIP 转为 FAIL）。
测试容器为 PLAINTEXT，无凭据。
