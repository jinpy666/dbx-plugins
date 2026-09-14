---
name: dbx-host-e2e
description: 用 shared/host-e2e 做宿主端到端验证：把插件 .dbxp 安装进隔离 app-data、拉起测试 DBX.app 验证插件中心与工作台。当用户要求端到端/e2e 验证插件、安装到宿主验证 UI、或排查安装管线（installer/激活记录/隔离数据目录）问题时使用。
---

# DBX 宿主端到端验证（shared/host-e2e）

`shared/host-e2e/install.sh` 封装宿主 worktree 的官方 PluginPackageInstaller
（checksum + 兼容性校验），对四插件通用；默认使用隔离 app-data，**绝不触碰
用户真实 DBX 数据目录**。

## 前置

- 宿主 checkout：仓库内 `host/` 子仓库（或 `DBX_HOST_WORKTREE` 指定；
  旧相邻路径自动兜底）。
- 插件已打包：`<plugin-dir>/dist/*.dbxp`（先跑该插件 `scripts/build.sh`）。

## 流程

```bash
# 1. 打包（以 files 为例）
files/scripts/build.sh

# 2. 安装进隔离 app-data 并拉起测试 DBX.app
shared/host-e2e/install.sh files              # 默认 app-data: shared/host-e2e/app-data
shared/host-e2e/install.sh files --reinstall  # 开发迭代：同版本重装
shared/host-e2e/install.sh files --no-restart # 只装不拉起

# 3. 拉起测试 DBX.app（宿主框架分支构建）——必须经 launch.sh：
shared/host-e2e/launch.sh                    # DBX_DATA_DIR=隔离 app-data 启动
```

- 指定其他存储：`--app-data <dir>` 或 `DBX_E2E_APP_DATA`；
  指定拉起的应用：`DBX_TEST_APP`。
- 测试数据都在 `shared/host-e2e/app-data/`（已 gitignore），可整目录删除重置。

## 拉起测试实例（DBX_DATA_DIR 红线）

宿主数据目录由 **DBX_DATA_DIR 环境变量**决定，`open` 无法传环境变量。
不带它启动 debug bundle 会读到**用户真实数据目录**（127+ 真实连接全量
可见），绝对禁止。一律用 `shared/host-e2e/launch.sh`（内部直接执行
bundle 内二进制并注入 DBX_DATA_DIR，日志落 `$APP_DATA/app-stdout.log`）。
校验要点：左栏只应出现隔离 app-data 里的 e2e 固件连接（如 vagrant），
出现真实连接列表立即退出。

安装脚本与启动脚本是两条独立命令：`install.sh` 只写插件存储
（`plugins/`），默认**不**拉起 app；`launch.sh` 负责带隔离数据目录拉起。

## 桌面 MCP 服务与插件工具暴露面

- **宿主 MCP 三个面（桌面 HTTP `mcp_http_server.rs`：端口 5225/token
  `mcp-http-token`/默认关、stdio `dbx-mcp --stdio`、npm
  `@dbx-app/mcp-server`）均只暴露 `dbx_*` 数据工具，不含插件工具**
  （真机 tools/list 实测）。启用桌面 HTTP：隔离 app-data 的 `dbx.db`
  `app_settings` 表 `mcp_http_server_settings` 写 `enabled:true` 后重启。
- **插件 MCP 工具经插件自身 standalone `--mcp` 暴露**（`dbx-plugin-ssh
  --mcp` 即本机 ZCode 的 `mcp__dbx-ssh__*`；ldap/files/kafka 同款模式）。
- 宿主本地凭据桥：`$APP_DATA/mcp-bridge-port` 记录端口（如 50002）。
- bundle 内 `Contents/MacOS/dbx` 是 tauri GUI 主程序，`dbx mcp` 不是
  子命令（只会拉起 GUI）；stdio 服务器是独立二进制 `dbx-mcp`。

## 安装语义与验收

- 存储布局：`plugins/<id>/versions/<ver>/` + `activations/<20位序号>-<uuid>.json`
  （`{sequence, version, previousVersion, packageSha256, activatedAt}`，camelCase）。
- 验收点：插件中心列表出现且版本正确 → 新建连接表单渲染 → 工作台可用 →
  卸载/重装激活记录正确。
- 双冒烟（以安装副本为准）：`DBX_PLUGIN_SIDECAR=<安装路径>/bin/darwin-arm64/<sidecar>
  python3 <plugin>/scripts/smoke_test.py`——确认装的二进制就是测的二进制。

## 常见坑

| 症状 | 处理 |
| --- | --- |
| installer 报 unknown field | example 二进制构建缓存过期：删
  `$DBX_HOST_WORKTREE/target/release/examples/install_plugin` 重编（脚本失败时会自动重试一次） |
| 测试 DBX.app 无插件框架 | 必须用宿主 worktree 的 debug bundle；正式版 /Applications/DBX.app 无效 |
| 宿主前端/Rust 改动未生效 | 宿主需 `pnpm tauri build --debug` 重建（约 10 分钟）；宿主上游依赖可能要求 rustc>=1.94，用 `RUSTUP_TOOLCHAIN=1.97.1` 覆盖（不影响插件族 1.88.0）；tauri 末尾报 TAURI_SIGNING_PRIVATE_KEY 缺失只影响更新包签名，.app 已产出可用 |
| 测试实例看到真实连接列表 | DBX_DATA_DIR 未注入：用 `launch.sh` 而不是 `open`；真实 DBX.app 与测试实例同 bundle id，`open` 会激活真实实例，GUI 走查前需先退出真实 DBX.app |
| 宿主 tauri 构建报 rustc 版本 | `RUSTUP_TOOLCHAIN=1.97.1 pnpm tauri build --debug`（rustup 已装 1.97.x） |
| 误装到真实数据目录 | 不要用 `--app-data` 指向 `~/Library/Application Support`；默认隔离目录即安全 |
