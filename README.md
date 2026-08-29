# DBX 插件族工作区（dbx-plugins）

tiny-rdm（`~/GolandProjects/tiny-rdm`）能力向 DBX 插件迁移的统一工作区。
**聚焦三插件，不做任何新插件**（决策存档见 `shared/CAPABILITY_CANDIDATES.zh-CN.md`）。

## 目录结构

```
dbx-plugins/
├── README.md          # 本文件（结构说明 + 实施入口）
├── AGENTS.md          # 工作区规约（硬性规则、文档入口、skills 索引）
├── .agents/skills/    # 触发式工程 skills：dbx-plugin-dev / dbx-host-e2e /
│                      #   dbx-ldap-dev / dbx-files-dev
├── shared/            # 公共目录：跨插件方案与基线文档（三插件共同遵循）
│   ├── PLUGIN_PROPOSAL_LDAP_RCLONE.zh-CN.md   # 总提案（定稿，含全部决策记录）
│   ├── IMPL_PLAN_M0_COMMON.zh-CN.md           # M0 公共基线（SDK 契约/lifecycle/审计/测试基建/roadmap）
│   ├── CAPABILITY_CANDIDATES.zh-CN.md         # 能力盘点（聚焦版：三插件边界 + 不迁移清单）
│   └── host-e2e/      # 插件无关的宿主 e2e 安装脚本（install.sh，隔离 app-data）
├── ssh/          # 插件 1：io.dbx.ssh（Rust）
│   └── docs/          # 本插件对标清单/协议/测试矩阵（含 batch-send 等增强跟踪）
├── ldap/              # 插件 2：io.dbx.ldap（Go sidecar）
│   ├── README.md
│   └── docs/IMPL_PLAN_DBX_LDAP.zh-CN.md       # 实施文档（M1/M3/M4 任务表）
└── files/             # 插件 3：io.dbx.files（Rust + Apache OpenDAL）
    ├── README.md
    └── docs/IMPL_PLAN_DBX_FILES.zh-CN.md      # 实施文档（M2/M3/M4 任务表）
```

## 三插件一览

| 插件 | ID | sidecar | 状态 | 实施文档 |
|---|---|---|---|---|
| ssh | `io.dbx.ssh` | Rust（russh，stdio-framed + 二进制帧） | **已上线 v0.2.x**，按自身对标清单增强 | 仓内 `docs/FEATURE_PARITY*.zh-CN.md` |
| ldap | `io.dbx.ldap` | Go（go-ldap + gokrb5，官方 Go SDK，stdio-jsonl） | 待实施（M0→M1→M3→M4） | `ldap/docs/IMPL_PLAN_DBX_LDAP.zh-CN.md` |
| files | `io.dbx.files` | Rust（opendal 0.58.x，dbx-plugin-sdk，stdio-framed + 二进制帧） | 待实施（M0→M2→M3→M4） | `files/docs/IMPL_PLAN_DBX_FILES.zh-CN.md` |

## 实施入口（给实施 agent）

1. 读 `shared/IMPL_PLAN_M0_COMMON.zh-CN.md` §6.0（实施顺序总览）与 §6.1（M0 任务清单）；
2. M0 验收后并行：LDAP M1（`ldap/docs` §9）∥ Files F2-0/F2-1（`files/docs` §11）；
3. 完成定义四件套：单测 + smoke + 对标清单更新 + 七语（ssh 仓 skill 硬性约定，沿用）；
4. 宿主侧不确定项记入 `ssh/docs/FRAMEWORK_FEEDBACK.zh-CN.md` 并按文档内降级方案继续。

## 相邻目录（不在本工作区内，路径约定）

- 宿主 worktree：`~/btroot/dbx-plugin-host-worktree`（插件框架分支 + 本地补丁）。
  ssh 的 `backend/Cargo.toml` 以 `[patch.crates-io]` 相对路径 `../../../dbx-plugin-host-worktree/…` 引用其 Rust SDK；files 插件照抄此模式。
- 参照源：`~/GolandProjects/tiny-rdm`（能力来源，只读）。
- ssh 开发规范 skill：`dbx-ssh-dev`（构建/测试/安装全流程，环境准备同源适用）。
