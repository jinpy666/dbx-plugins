---
name: dbx-host-e2e
description: 用 shared/host-e2e 做宿主端到端验证：把插件 .dbxp 安装进隔离 app-data、拉起测试 DBX.app 验证插件中心与工作台。当用户要求端到端/e2e 验证插件、安装到宿主验证 UI、或排查安装管线（installer/激活记录/隔离数据目录）问题时使用。
---

# DBX 宿主端到端验证（shared/host-e2e）

`shared/host-e2e/install.sh` 封装宿主 worktree 的官方 PluginPackageInstaller
（checksum + 兼容性校验），对三插件通用；默认使用隔离 app-data，**绝不触碰
用户真实 DBX 数据目录**。

## 前置

- 宿主 worktree：`~/btroot/dbx-plugin-host-worktree`（或 `DBX_HOST_WORKTREE` 指定）。
- 插件已打包：`<plugin-dir>/dist/*.dbxp`（先跑该插件 `scripts/build.sh`）。

## 流程

```bash
# 1. 打包（以 files 为例）
files/scripts/build.sh

# 2. 安装进隔离 app-data 并拉起测试 DBX.app
shared/host-e2e/install.sh files              # 默认 app-data: shared/host-e2e/app-data
shared/host-e2e/install.sh files --reinstall  # 开发迭代：同版本重装
shared/host-e2e/install.sh files --no-restart # 只装不拉起

# 3. 手动打开测试 DBX.app（宿主框架分支构建）：
open ~/btroot/dbx-plugin-host-worktree/target/debug/bundle/macos/DBX.app
```

- 指定其他存储：`--app-data <dir>` 或 `DBX_E2E_APP_DATA`；
  指定拉起的应用：`DBX_TEST_APP`。
- 测试数据都在 `shared/host-e2e/app-data/`（已 gitignore），可整目录删除重置。

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
| 宿主前端/Rust 改动未生效 | 宿主需 `pnpm tauri build --debug` 重建（约 10 分钟） |
| 误装到真实数据目录 | 不要用 `--app-data` 指向 `~/Library/Application Support`；默认隔离目录即安全 |
