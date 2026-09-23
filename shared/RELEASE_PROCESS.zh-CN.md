# 四插件独立发布流程

本仓库是四插件源码和集成验证仓；`ssh/`、`ldap/`、`files/`、`kafka/` 是四个
独立发行单元。一个插件可以单独变更、单独测试、单独生成候选包和单独提交
DBX Marketplace，不要求另外三个插件同时发版。

## 版本和 tag

版本唯一来源是对应插件的 `manifest.json`。native sidecar 的版本必须与
Manifest 保持一致，不能在 GitHub Actions 中隐式递增版本，也不能覆盖已经发布
的版本。

```text
ssh-v0.4.71
ldap-v0.1.67
files-v0.1.57
kafka-v0.1.38
```

创建 GitHub Release 前，先更新对应插件的 changelog、协议文档、PROGRESS 文档
和七语文案，然后运行：

```bash
python3 shared/release/validate_manifests.py ssh --tag ssh-v0.4.71
ssh/scripts/test.sh --skip-host
```

其他插件替换插件名和测试命令即可。工作区级联测仍使用：

```bash
shared/test-all.sh
```

## GitHub Release

根目录 `.github/workflows/release-*.yml` 通过 DBX 官方 reusable workflow 构建：

- `darwin-arm64`
- `darwin-x64`
- `windows-x64`
- `linux-x64`
- `linux-arm64`

四个插件都有 native sidecar，因此不能使用 `universal`。作者 CI 产生的是
unsigned review candidate，包含 `.dbxp` 和 `release-candidates.json`，不包含
`signature.json`，不能直接当作官方市场安装包。

## DBX Store 自动同步（2026-09 起）

插件仓发布 GitHub Release 后，DBX Store 自动同步上架：不再向 `t8y2/dbx-store`
提交审核 Issue，也无需手工候选 PR 或 catalog PR。发布即提交，发布前必须确认：

1. tag 与 `manifest.json` 版本一致且不可复用；
2. `release-candidates.json` 与各平台候选包（`.dbxp`、`.artifact.json`）完整；
3. manifest 的权限、许可证、主页、支持地址等元数据准确（自动同步以其为准）；
4. changelog、协议文档、PROGRESS 文档与七语文案已更新。

发布后在 catalog 核对 plugin ID、version 与签名产物信息。签名与 catalog 发布
由官方自动化完成；插件作者不管理官方签名私钥，也不在源码仓或插件包中放置
`signature.json`。

## 本地发布和市场发布的边界

`scripts/release-local.sh` 只用于本地 DBX.app、隔离 app-data 和四插件联合验证。
它不是官方市场发布入口。市场发布必须经过：

```text
manifest version → plugin tag → GitHub Release
→ DBX Store 自动同步（签名与上架）
```

宿主同步或 DBX.app 更新后仍必须运行：

```bash
scripts/host-sync.sh contract
```

契约适配改在 `shared/frontend/`，不要复制到四个插件中。

## 本地一键发布与变更检测（release-local.sh）

`ssh/`、`ldap/`、`files/`、`kafka/`、`host/` 均为 git submodule；`scripts/release-local.sh`
是本地联合发布入口（构建 DBX.app + 四插件、装入 app-data、拉起），不是市场发布入口：

```bash
scripts/release-local.sh --plan          # 只看变更与计划，无任何副作用
scripts/release-local.sh                 # 只重建/重装有变化的插件
scripts/release-local.sh ssh files       # 只处理指定插件（host 仍按自身跳过逻辑）
scripts/release-local.sh --skip-host     # 复用 /Applications/DBX.app
scripts/release-local.sh --isolated      # 装入 shared/host-e2e/app-data（不动真实数据）
scripts/release-local.sh --reinstall-plugins --no-launch   # 开发迭代重装
```

变更检测（指纹）在每个插件**子仓内部**计算：覆盖全部已跟踪 + 未跟踪源文件
（排除 `docs/`、`.worktrees/`、manifest/Cargo 版本文件），并记录子仓 HEAD；
宿主指纹含 HEAD + 工作区 dirty 内容哈希。指纹与日志存于 `.release-state/`
（已 gitignore），删除即重置。注意：2026-09-18 submodule 化后指纹基准变更，
首次运行会把四个插件全部重建一次，属预期。

## 工作区垃圾回收（gc.sh）

`scripts/gc.sh` 在每次成功 release 后自动执行（`--no-gc` 跳过），也可单独运行：

```bash
scripts/gc.sh --dry-run    # 只列出将释放的空间
scripts/gc.sh              # 实际清理
```

清理范围（全部为可再生内容，不碰源码与指纹）：休眠的 Rust target 缓存、
增量缓存、过期 `.mimosa` 会话、过期日志、`__pycache__`、`.DS_Store`、
各插件 `dist/` 中除最新 `GC_DIST_KEEP`（默认 3）个外的旧 `.dbxp` 及其
`.artifact.json`、docker 构建临时上下文。阈值经环境变量覆盖：
`GC_STALE_DAYS=7 GC_SESSION_DAYS=3 GC_LOG_DAYS=14 GC_DIST_KEEP=3`。
