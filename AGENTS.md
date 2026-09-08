# DBX 插件族工作区规约（AGENTS.md）

DBX 插件族统一工作区，聚焦四插件：
`ssh/`（io.dbx.ssh，Rust，已上线 v0.2.x）、`ldap/`（io.dbx.ldap，
Go）、`files/`（io.dbx.files，Rust + OpenDAL）、`kafka/`（io.dbx.kafka，
Go + franz-go）。**不做任何新插件**（决策存档
`shared/CAPABILITY_CANDIDATES.zh-CN.md`；2026-09-05 经用户决策新增
kafka 插件，见 `kafka/docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md`）。
四插件能力已全面超越早期外部基线（历史对标记录已随基线退役删除），
后续以插件自身需求演进，不再参考外部实现。

## 结构与文档入口

- 根 `README.md`：目录结构、插件一览、实施 agent 入口。
- `shared/`：跨插件方案与基线（总提案、M0 公共基线、能力盘点）；
  **`shared/frontend/` 是宿主适配公共代码唯一实现点**（binary 事件归一化、
  主题令牌兜底等），插件前端相对引用、不各抄一份，见其 README。
- `host/`：DBX 宿主子仓库（git submodule → t8y2/dbx，上游为主、本地二开
  补丁跟随）；同步入口 `scripts/host-sync.sh`，红线与流程见
  `shared/PROGRESS-HOST-SUBREPO.zh-CN.md`。
- 各插件 `docs/IMPL_PLAN_*.zh-CN.md` 是该插件唯一工作来源；
  `docs/PROGRESS-*.zh-CN.md` 随任务收尾更新。

## 任务触发式 skills（.agents/skills/）

| Skill | 用途 |
| --- | --- |
| `dbx-plugin-dev` | 插件族通用开发规范与构建/测试/打包/安装全流程（硬性约定、宿主契约监察、故障速查） |
| `dbx-host-e2e` | shared/host-e2e 宿主端到端验证（隔离 app-data 安装） |
| `dbx-ldap-dev` | LDAP 插件专项（Go、stdio-jsonl、认证矩阵、测试容器） |
| `dbx-files-dev` | Files 插件专项（OpenDAL、二进制通道、异步 job 传输） |
| `dbx-kafka-dev` | Kafka 插件专项（Go + franz-go、stdio-jsonl、dev 测试集群、Schema Registry） |
| `dbx-web-docker` | web Docker 发版（deploy/web 镜像构建、插件自动安装、服务器部署） |
| `dbx-ssh-sftp-dev`（用户级） | ssh 插件专项（russh、sudo/sftp_ext、宿主 worktree 细节） |

## 硬性规则

1. 完成定义四件套：单测 + smoke + 对标/任务清单更新 + 七语文案，缺一不算完成。
2. 协议方法 `<域>/<动作>`、字段 camelCase、同族参数一致；新方法必须同步协议文档。
3. 宿主 1.1 特性 optional 降级；不重复宿主能力；凭据走 secret binding 不持久化。
4. 仓库卫生：截图（`*.png`、`screenshots/`）、`__pycache__`、node_modules、
   dist/target/ui、工具状态目录、e2e 运行时数据一律不入库（根 `.gitignore` 已覆盖；
   新增产物类型先补忽略规则再提交）。测试临时产物不落在插件根目录，入 `docs/` 或删除。
5. smoke 中未实现/未注册的方法 SKIP 而非 FAIL；并行开发期的后端失败记入
   对应 PROGRESS 文档而非阻塞其他路径。
6. 验证顺序：改动哪层跑哪层最小验证（前端 typecheck/test，后端 cargo/go test），
   交付前再跑该插件 `scripts/test.sh`。
7. **宿主异动跟进**：宿主上游权威，契约可能在任意一次同步/正式版更新中变更。
   每次 `scripts/host-sync.sh sync` 或 DBX.app 更新后必跑
   `scripts/host-sync.sh contract`，有契约 diff 就在 **`shared/frontend/` 公共
   适配层单点修改**（插件引用处自然生效；确需插件内改动的说明理由），同步
   `env.d.ts` 类型、插件 mock（必须镜像真实桥当前形状，防单测脱节），并真机
   复验受影响流。宿主新特性按"optional 降级"接入（规则 3），跟进状态记对应
   PROGRESS 文档。
