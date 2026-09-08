# DBX 插件族工作区（dbx-plugins）

DBX 插件族统一工作区。
**聚焦四插件，不做任何新插件**（决策存档见 `shared/CAPABILITY_CANDIDATES.zh-CN.md`；
2026-09-05 经用户决策新增 kafka 插件，见 `kafka/docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md`）。

## 目录结构

```
dbx-plugins/
├── README.md          # 本文件（结构说明 + 实施入口）
├── AGENTS.md          # 工作区规约（硬性规则、文档入口、skills 索引）
├── .agents/skills/    # 触发式工程 skills：dbx-plugin-dev / dbx-host-e2e /
│                      #   dbx-ldap-dev / dbx-files-dev / dbx-kafka-dev / dbx-web-docker
├── shared/            # 公共目录：跨插件方案与基线文档（插件族共同遵循）
│   ├── IMPL_PLAN_M0_COMMON.zh-CN.md           # M0 公共基线（SDK 契约/lifecycle/审计/测试基建/roadmap）
│   ├── CAPABILITY_CANDIDATES.zh-CN.md         # 能力盘点（聚焦版：三插件边界 + 不迁移清单）
│   └── host-e2e/      # 插件无关的宿主 e2e 安装脚本（install.sh，隔离 app-data）
├── scripts/           # 工作区级脚本：release-local / release-docker / host-sync
├── host/              # DBX 宿主子仓库（git submodule → t8y2/dbx，插件框架分支 + 本地二开提交）
├── deploy/web/        # DBX web Docker 发版（Dockerfile + entrypoint + compose，
│                      #   四插件 Linux .dbxp 烤进镜像，容器首启自动安装）
├── ssh/          # 插件 1：io.dbx.ssh（Rust）
│   └── docs/          # 本插件对标清单/协议/测试矩阵（含 batch-send 等增强跟踪）
├── ldap/              # 插件 2：io.dbx.ldap（Go sidecar）
│   ├── README.md
│   └── docs/IMPL_PLAN_DBX_LDAP.zh-CN.md       # 实施文档（M1/M3/M4 任务表）
├── files/             # 插件 3：io.dbx.files（Rust + Apache OpenDAL）
│   ├── README.md
│   └── docs/IMPL_PLAN_DBX_FILES.zh-CN.md      # 实施文档（M2/M3/M4 任务表）
└── kafka/             # 插件 4：io.dbx.kafka（Go sidecar + franz-go）
    ├── README.md
    └── docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md      # 实施文档（Phase 1，2026-09 新增）
```

## 插件一览

| 插件 | ID | sidecar | 状态 | 实施文档 |
|---|---|---|---|---|
| ssh | `io.dbx.ssh` | Rust（russh，stdio-framed + 二进制帧） | **已上线 v0.2.x**，按自身对标清单增强 | 仓内 `docs/FEATURE_PARITY*.zh-CN.md` |
| ldap | `io.dbx.ldap` | Go（go-ldap + gokrb5，官方 Go SDK，stdio-jsonl） | 待实施（M0→M1→M3→M4） | `ldap/docs/IMPL_PLAN_DBX_LDAP.zh-CN.md` |
| files | `io.dbx.files` | Rust（opendal 0.58.x，dbx-plugin-sdk，stdio-framed + 二进制帧） | 待实施（M0→M2→M3→M4） | `files/docs/IMPL_PLAN_DBX_FILES.zh-CN.md` |
| kafka | `io.dbx.kafka` | Go（franz-go + kadm，官方 Go SDK，stdio-jsonl） | 商用级可用（2026-09-05：45+11 方法，Confluent+AWS Glue 双 SR、Kerberos/ZK、ag-grid 检索，test.sh 全绿） | `kafka/docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md` |

## 实施入口（给实施 agent）

1. 读 `shared/IMPL_PLAN_M0_COMMON.zh-CN.md` §6.0（实施顺序总览）与 §6.1（M0 任务清单）；
2. M0 验收后并行：LDAP M1（`ldap/docs` §9）∥ Files F2-0/F2-1（`files/docs` §11）；
3. 完成定义四件套：单测 + smoke + 对标清单更新 + 七语（ssh 仓 skill 硬性约定，沿用）；
4. 宿主侧不确定项按 optional 降级处理，记录进对应插件 PROGRESS 文档。

## 宿主子仓库与相邻目录

- 宿主子仓库：`host/`（git submodule → `https://github.com/t8y2/dbx.git`，分支
  `dev/plugin-framework-current` = origin/main + 少量本地二开提交）。策略：
  **上游为主、本地补丁跟随**——用 `scripts/host-sync.sh sync` 定期合并
  origin/main 保留二开提交，待上游功能覆盖所需后逐个移除二开提交。
  ssh/files 的 `backend/Cargo.toml` 以 `[patch.crates-io]` 相对路径
  `../../host/plugins/sdk/rust/dbx-plugin-sdk` 引用其 Rust SDK。
  旧相邻路径 `~/btroot/dbx-plugin-host-worktree` 保留为指向 `host/` 的符号链接，
  兼容历史文档与外部引用。
- 历史基线：四插件能力已全面超越早期外部参照（历史对标记录已随基线退役删除），
  后续不再参考外部实现。
- ssh 专项 skill：`dbx-ssh-sftp-dev`（用户级，构建/测试/安装全流程，环境
  准备同源适用）。

## 发版

- **日常跟上游**：`scripts/host-sync.sh sync`（宿主子仓库拉取并合并上游，
  保留本地二开提交；完成后在父仓库提交 submodule 指针）。
- **本地 macOS 应用**：`scripts/release-local.sh`（指纹比对，host/插件无变更
  自动跳过；四插件并发构建；默认不再拉上游，`--sync-host` 手动同步；完成后
  发布 `/Applications` → 安装有变更的插件 → 拉起）。
- **服务器 web 访问**：`scripts/release-docker.sh`（构建含四插件 Linux .dbxp 的
  dbx-web 镜像，容器首启自动安装；机制与部署见 skill `dbx-web-docker` 与
  `deploy/web/`）。
