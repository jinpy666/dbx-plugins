---
name: dbx-plugin-dev
description: DBX 插件族（ssh / ldap / files / kafka）通用开发规范与构建、测试、打包、安装全流程。当用户要求开发、构建、测试、打包或安装任一插件，排查 sidecar / 打包 / 安装问题，或需要确认插件工程硬性约定（七语、完成定义四件套、协议命名、安全红线）时使用。
---

# DBX 插件族通用开发规范

工作区 `~/btroot/dbx-plugins` 聚焦四插件：`ssh/`（Rust，已上线）、
`ldap/`（Go，stdio-jsonl）、`files/`（Rust + OpenDAL，stdio-framed）、
`kafka/`（Go + franz-go，stdio-jsonl）。
每个插件结构统一：`manifest.json` + `dbx-plugin.toml` + `frontend/`（Vue/Vite）
+ `backend/`（sidecar）+ `scripts/`（自动化）+ `docs/`。

各插件专项入口见 skills：`dbx-ssh-sftp-dev`（用户级）、`dbx-ldap-dev`、
`dbx-files-dev`、`dbx-kafka-dev`、`dbx-host-e2e`。

## 环境准备（一次性）

```bash
# Node 经 nvm；pnpm 在 ~/Library/pnpm；Rust 固定 1.88.0（宿主要求 rust-version）
export PATH="$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$HOME/.cargo/bin:$PATH"
npm install -g @dbx-app/plugin-cli @dbx-app/cli @dbx-app/mcp-server
# 插件本地打包不需要 DBX 源码：CLI 自带 SDK（sdk-root）。
# 仅宿主集成验证需要 host/ 子仓库（scripts/host-sync.sh init；
# 旧相邻路径 ~/btroot/dbx-plugin-host-worktree 为指向 host/ 的符号链接）
```

## 硬性约定（四插件共同遵守）

1. **协议命名**：sidecar 方法 `<域>/<动作>`；参数/返回字段 camelCase；同族方法
   参数保持一致。新增方法必须同步该插件 `docs/` 下的协议文档。
2. **兼容基线**：目标 Host API 1.0（`engines.host_api: ">=1.0.0"`）。宿主 1.1
   特性（appearance/locale、fileTransfer、workbenchState）一律 optional 降级，
   缺失时功能可降但不可死；能用宿主能力（如 `visible_when`）就不自己造。
   **binary 事件双形状**：宿主桥 b15281024（随 DBX.app 0.6.2）把沙箱 binary
   事件从 `{ channel, dataBase64 }` 改为零拷贝 `{ channel, data: Uint8Array }`，
   `dataBase64` 字段已不存在。插件 onBinary 消费必须经
   **`shared/frontend/binaryEvent.ts`** 的 `bridgeBinaryBytes` 归一化（优先
   `data`，回退 base64；宿主适配公共代码一律进 `shared/frontend/` 单点维护，
   插件相对引用，见其 README，禁止各插件抄一份）；`env.d.ts` 两字段都要
   optional；插件自带 mockDbxHost/mockHost 必须镜像真实桥当前形状，否则单测
   全绿照样翻车。
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

本地联合发布统一走 `scripts/release-local.sh`：先 `--plan` 看变更与计划
（无副作用），再默认运行只重建/重装有变化的插件；指纹按 submodule 内部
计算，`.release-state/` 可删可重置。每次 release 收尾自动跑
`scripts/gc.sh`（`--no-gc` 跳过；也可 standalone / `--dry-run`）：清理 Rust
构建缓存、旧日志、旧 dist 包、插件 store 旧版本（真实 + e2e app-data，默认
保留最新 3 版并保护当前激活版）、docker 冒烟残留。
市场发布：打 `<plugin>-v<version>` tag、发布 GitHub Release 即由 DBX Store
自动同步上架（2026-09 起，无需审核 Issue/候选 PR），发布后核对 catalog 版本；
流程见 `shared/RELEASE_PROCESS.zh-CN.md`。

## 宿主契约监察（每次 host sync / DBX.app 更新后必做）

宿主是上游权威（host/ 子模块随 origin/main 演进），**插件面对的宿主契约可能
在任意一次同步中变更**。每次 `scripts/host-sync.sh sync` 或正式版 DBX.app
更新后：

```bash
scripts/host-sync.sh contract   # 契约文件 diff + 当前桥形状快照 + 跟进清单
```

监察面（脚本内 CONTRACT_PATHS）：pluginHostBridge（桥/沙箱 SDK）、
pluginFieldConditions（visible_when 等）、pluginAppearance、pluginFilesystem、
frontendPlugin、crates/dbx-core/src/plugins/host.rs（manifest/生命周期）。
有 diff 就在 **`shared/frontend/` 公共适配层单点修改**（含接入新插件的
onBinary 消费方），同步各插件 `env.d.ts` 类型与 mock 镜像、真机复验受影响
流（终端回显、下载分块、拖放）。案例：b15281024 静默改 binary 事件形状，0.6.2
上所有插件终端/下载流全灭（修复与收敛记录 ssh/docs/PROGRESS-P-SSH.zh-CN.md
§8.11、shared/frontend/README.zh-CN.md）。

## 常见故障速查

| 症状 | 根因 |
| --- | --- |
| 打包报 Cargo.lock v4 does not understand | /usr/local/bin 旧 cargo 1.69 抢占 PATH；确保 $HOME/.cargo/bin 在前 |
| 打包报 lockfile `--locked` | 新依赖未入 lockfile，用 CLI 同款 patch 参数 regenerate |
| 安装后插件中心报 unknown field | installer example 二进制有构建缓存，删掉 install_plugin 强制重编 |
| Go 插件打包报 go.work 与模块 go 版本冲突 | env 残留 DBX_PLUGIN_SDK_ROOT；`env -u DBX_PLUGIN_SDK_ROOT` 直调 plugin-cli 二进制 |
| dbx-plugin: command not found | nvm PATH 未加载 |
| 工作台永停"正在连接" | 宿主桥 DataCloneError（Vue Proxy 不可 structuredClone）或 sidecar 未注册该方法 |
| 连接成功但终端/下载流零数据（invoke 类正常） | 宿主桥 binary 事件契约变更（data vs dataBase64）：插件 onBinary 消费没走 `shared/frontend/binaryEvent.ts` 的 `bridgeBinaryBytes` 双形状归一化；对装好的 sidecar 跑 smoke 可确认后端帧正常，问题在前端桥层 |

## 仓库规约

- 截图、`__pycache__`、node_modules、dist/target/ui、工具状态（.mimosa/.omc/
  .playwright-mcp）、e2e 运行时数据（shared/host-e2e/app-data|logs）一律不入库
  （根 `.gitignore` 已覆盖，新增产物类型先补忽略规则）。
- 文档改动与代码改动同一次提交；进度文档（docs/PROGRESS-*.zh-CN.md）随任务收尾更新。
