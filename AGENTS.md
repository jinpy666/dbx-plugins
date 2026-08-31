# DBX 插件族工作区规约（AGENTS.md）

tiny-rdm 能力向 DBX 插件迁移的统一工作区，聚焦三插件：
`ssh/`（io.dbx.ssh，Rust，已上线 v0.2.x）、`ldap/`（io.dbx.ldap，
Go）、`files/`（io.dbx.files，Rust + OpenDAL）。**不做任何新插件**（决策存档
`shared/CAPABILITY_CANDIDATES.zh-CN.md`）。

## 结构与文档入口

- 根 `README.md`：目录结构、三插件一览、实施 agent 入口。
- `shared/`：跨插件方案与基线（总提案、M0 公共基线、能力盘点）。
- 各插件 `docs/IMPL_PLAN_*.zh-CN.md` 是该插件唯一工作来源；
  `docs/PROGRESS-*.zh-CN.md` 随任务收尾更新。

## 任务触发式 skills（.agents/skills/）

| Skill | 用途 |
| --- | --- |
| `dbx-plugin-dev` | 三插件通用开发规范与构建/测试/打包/安装全流程（硬性约定、故障速查） |
| `dbx-host-e2e` | shared/host-e2e 宿主端到端验证（隔离 app-data 安装） |
| `dbx-ldap-dev` | LDAP 插件专项（Go、stdio-jsonl、认证矩阵、测试容器） |
| `dbx-files-dev` | Files 插件专项（OpenDAL、二进制通道、异步 job 传输） |
| `dbx-web-docker` | web Docker 发版（deploy/web 镜像构建、插件自动安装、服务器部署） |
| `dbx-ssh-dev`（用户级） | ssh 插件专项（russh、sudo/sftp_ext、宿主 worktree 细节） |

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
