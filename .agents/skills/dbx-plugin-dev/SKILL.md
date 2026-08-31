---
name: dbx-plugin-dev
description: DBX 插件族（ssh / ldap / files）通用开发规范与构建、测试、打包、安装全流程。当用户要求开发、构建、测试、打包或安装任一插件，排查 sidecar / 打包 / 安装问题，或需要确认插件工程硬性约定（七语、完成定义四件套、协议命名、安全红线）时使用。
---

# DBX 插件族通用开发规范

工作区 `~/btroot/dbx-plugins` 聚焦三插件：`ssh/`（Rust，已上线）、
`ldap/`（Go，stdio-jsonl）、`files/`（Rust + OpenDAL，stdio-framed）。
每个插件结构统一：`manifest.json` + `dbx-plugin.toml` + `frontend/`（Vue/Vite）
+ `backend/`（sidecar）+ `scripts/`（自动化）+ `docs/`。

各插件专项入口见 skills：`dbx-ssh-dev`（用户级）、`dbx-ldap-dev`、
`dbx-files-dev`、`dbx-host-e2e`。

## 环境准备（一次性）

```bash
# Node 经 nvm；pnpm 在 ~/Library/pnpm；Rust 固定 1.88.0（宿主要求 rust-version）
export PATH="$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$HOME/.cargo/bin:$PATH"
npm install -g @dbx-app/plugin-cli @dbx-app/cli @dbx-app/mcp-server
# 插件本地打包不需要 DBX 源码：CLI 自带 SDK（sdk-root）。
# 仅宿主集成验证需要 host/ 子仓库（scripts/host-sync.sh init；
# 旧相邻路径 ~/btroot/dbx-plugin-host-worktree 为指向 host/ 的符号链接）
```

## 硬性约定（三插件共同遵守）

1. **协议命名**：sidecar 方法 `<域>/<动作>`；参数/返回字段 camelCase；同族方法
   参数保持一致。新增方法必须同步该插件 `docs/` 下的协议文档。
2. **兼容基线**：目标 Host API 1.0（`engines.host_api: ">=1.0.0"`）。宿主 1.1
   特性（appearance/locale、fileTransfer、workbenchState）一律 optional 降级，
   缺失时功能可降但不可死；能用宿主能力（如 `visible_when`）就不自己造。
3. **不重复宿主**：隧道/代理/传输走 DBX 传输层（sidecar 经 `runtime.host:port`
   拨号）；凭据走宿主 secret binding，插件不持久化；禁止新增与宿主重叠的表单字段。
4. **安全红线**：写操作过只读策略（ensure_writable / DN 白名单 / 屏蔽属性）；
   删除类操作拒绝根路径；私钥/凭据只出指纹或摘要不出内容；审计事件按
   `shared/IMPL_PLAN_M0_COMMON.zh-CN.md` 基线。
5. **前端**：新文案七语全补（zh-CN/zh-TW/en/es/it/ja/pt，漏一语 typecheck 不报，
   靠 review）；错误统一走 showError 系；无新依赖须说明理由。
6. **完成定义四件套**：单测（纯解析，不连远端）+ smoke 用例（未注册方法 SKIP
   而非 FAIL）+ 对标/任务清单状态更新 + 七语文案，四者齐才算完成。

## 构建与测试（各插件 scripts/ 下，命令同构）

```bash
scripts/build.sh        # 前端三件套(typecheck/test/build) + sidecar 构建 + dbx-plugin package
scripts/test.sh         # 全套：单测 → 前端三件套 → 构建 → 打包 → smoke（all green 才算过）
scripts/smoke_*.py      # 无 UI 全链路冒烟；容器类用例无容器时自动 SKIP
scripts/install.sh      # ssh 专有；其余插件用 shared/host-e2e/install.sh
```

- 打包前保证 `~/.cargo/bin` 在 PATH 首位（旧 cargo 抢占会报 Cargo.lock v4 错误）；
  打包时 `unset DBX_PLUGIN_SDK_ROOT`（脚本已处理）。
- 新依赖必须进 lockfile（Cargo.lock / pnpm-lock.yaml / go.sum），否则 `--locked` 打包失败。

## 安装与交付（插件无关）

```bash
shared/host-e2e/install.sh <plugin-dir>              # 隔离 app-data 安装，不动真实 DBX 数据
shared/host-e2e/install.sh <plugin-dir> --reinstall  # 同版本重装（开发迭代）
shared/host-e2e/install.sh <plugin-dir> --no-restart # 不拉起测试 DBX.app
```

详见 skill `dbx-host-e2e`。

## 常见故障速查

| 症状 | 根因 |
| --- | --- |
| 打包报 Cargo.lock v4 does not understand | /usr/local/bin 旧 cargo 1.69 抢占 PATH；确保 $HOME/.cargo/bin 在前 |
| 打包报 lockfile `--locked` | 新依赖未入 lockfile，用 CLI 同款 patch 参数 regenerate |
| 安装后插件中心报 unknown field | installer example 二进制有构建缓存，删掉 install_plugin 强制重编 |
| Go 插件打包报 go.work 与模块 go 版本冲突 | env 残留 DBX_PLUGIN_SDK_ROOT；`env -u DBX_PLUGIN_SDK_ROOT` 直调 plugin-cli 二进制 |
| dbx-plugin: command not found | nvm PATH 未加载 |
| 工作台永停"正在连接" | 宿主桥 DataCloneError（Vue Proxy 不可 structuredClone）或 sidecar 未注册该方法 |

## 仓库规约

- 截图、`__pycache__`、node_modules、dist/target/ui、工具状态（.mimosa/.omc/
  .playwright-mcp）、e2e 运行时数据（shared/host-e2e/app-data|logs）一律不入库
  （根 `.gitignore` 已覆盖，新增产物类型先补忽略规则）。
- 文档改动与代码改动同一次提交；进度文档（docs/PROGRESS-*.zh-CN.md）随任务收尾更新。
