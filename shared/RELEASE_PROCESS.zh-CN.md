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

## DBX Store 提交

候选包发布到 GitHub Release 后，向 `t8y2/dbx-store` 提交审核 Issue，提供：

1. 插件源码仓库和精确 tag；
2. `release-candidates.json`；
3. 权限、变更说明、许可证、主页和支持地址；
4. 各平台候选包的 URL、SHA-256 和 size。

审核通过后由 DBX Store 保护环境完成签名，再提交 catalog PR。插件作者不管理
官方签名私钥，也不在源码仓或插件包中放置 `signature.json`。

## 本地发布和市场发布的边界

`scripts/release-local.sh` 只用于本地 DBX.app、隔离 app-data 和四插件联合验证。
它不是官方市场发布入口。市场发布必须经过：

```text
manifest version → plugin tag → GitHub Release candidate
→ DBX Store review/signing → catalog PR
```

宿主同步或 DBX.app 更新后仍必须运行：

```bash
scripts/host-sync.sh contract
```

契约适配改在 `shared/frontend/`，不要复制到四个插件中。
