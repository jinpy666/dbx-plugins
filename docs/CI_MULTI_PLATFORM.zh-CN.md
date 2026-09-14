# 多平台 CI 与发布产出

本仓库使用 DBX 官方插件 CLI 和官方 reusable workflow 约定构建原生插件。
原生 sidecar 不做“跨编译后伪装 target”：每个 DBX target 由对应 GitHub Actions
runner 构建，避免 Rust/Go 依赖、系统库和运行时 ABI 不匹配。

## 支持的五个 target

| DBX target | Runner | 产物示例 |
|---|---|---|
| `darwin-arm64` | `macos-15` | Apple Silicon |
| `darwin-x64` | `macos-15-intel` | Intel macOS |
| `windows-x64` | `windows-2022` | Windows x64 |
| `linux-x64` | `ubuntu-24.04` | Linux x64 |
| `linux-arm64` | `ubuntu-24.04-arm` | Linux ARM64 |

四个插件各生成五个未签名候选包和对应 `.artifact.json`。命名格式为：

```text
io.dbx.<plugin>-<version>-<target>.dbxp
io.dbx.<plugin>-<version>-<target>.artifact.json
```

## CI 工作流

### Pull request / main 构建

`.github/workflows/build-candidates.yml` 使用 20 个并发矩阵任务：

```text
4 plugins × 5 native targets = 20 builds
```

每个任务会：

1. 安装固定版本 `@dbx-app/plugin-cli`。
2. 设置 Node.js、Go 和 Rust 工具链。
3. 设置 `DBX_PLUGIN_TARGET`。
4. 运行 `dbx-plugin package .`。
5. 上传 `.dbxp` 和 `.artifact.json`。

最后的 `verify` job 会下载所有候选包，校验插件身份、target 覆盖、文件大小、
SHA-256 和未签名状态，并生成一个合并 artifact。

### GitHub Release 发布

四个 `release-*.yml` 继续调用 DBX 官方
`t8y2/dbx/.github/workflows/plugin-release-reusable.yml@plugin-sdk-v1`。
该 workflow 同样使用五平台矩阵，并把候选包上传到对应 GitHub Release。

候选包是 unsigned review candidate，不应在本仓库 CI 中放入签名私钥。正式安装包
由 DBX Store 或受控仓库完成审核、签名和目录发布。

## 本地复现

在对应平台上执行：

```bash
npm install --global @dbx-app/plugin-cli@0.1.3
export DBX_PLUGIN_TARGET=darwin-arm64  # 按当前机器替换
cd ssh
dbx-plugin package .
```

不应在 macOS runner 上设置 `linux-x64` 或 `windows-x64`。官方 CLI 会拒绝 native
build host 与 target 不一致的请求；这正是 CI 使用原生 runner 矩阵的原因。

## 官方依据

- [DBX plugin publishing](https://github.com/t8y2/dbx/blob/main/host/plugins/RELEASING.md)
- [DBX plugin CLI guide](https://github.com/t8y2/dbx/blob/main/host/plugins/sdk/cli/README.md)
- [GitHub Actions workflow syntax](https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions)
- [GitHub Actions artifacts](https://docs.github.com/en/actions/using-workflows/storing-workflow-data-as-artifacts)
- [Rust platform support](https://doc.rust-lang.org/rustc/platform-support.html)
- [Go build environment](https://go.dev/doc/install/source#environment)
