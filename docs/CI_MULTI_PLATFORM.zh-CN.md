# 多平台 CI 与发布产出

本仓库使用 DBX 官方插件 CLI 和官方 reusable workflow 约定构建原生插件。
原生 sidecar 不做“跨编译后伪装 target”：每个 DBX target 由对应 GitHub Actions
runner 构建，避免 Rust/Go 依赖、系统库和运行时 ABI 不匹配。例外是 Linux 的
C 运行时基线：构建 runner 的 glibc 只能比运行环境新、不能比它旧，因此 Linux
target 的链接走 cargo-zigbuild 固定低 glibc 基线（见下文「Linux glibc 基线」）。

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

## Linux glibc 基线（web/docker 安装的关键约束）

DBX 官方 web docker 镜像的运行时是 `debian:bookworm-slim`（glibc 2.36）。
在 ubuntu-24.04（glibc 2.39）上原生构建的 Rust sidecar 会钉死
`GLIBC_2.38`/`GLIBC_2.39` 符号（`__isoc23_sscanf`、`pidfd_spawnp` 等），
动态加载器在 `main()` 之前即失败，宿主侧表现为
`Plugin 'io.dbx.ssh' initialization failed: exited with status 1`
（dbx-plugin-ssh#8、#58，files 插件同因）。为此 CI 在 Linux runner 上：

1. 安装 `ziglang`（pip）+ `cargo-zigbuild`，并放一个 `cargo` 包装器到 PATH
   最前：CLI 打包裸调的 `cargo build` 被转成 `cargo zigbuild --target
   <宿主triple>`（链接走 zig，固定低 glibc 基线，实测 2.30），再把产物从
   `<triple>/release/` 镜像回 CLI 拷贝期待的 `<CARGO_TARGET_DIR>/release/`。
   注意 **必须显式传 `--target`**：cargo-zigbuild 不带 `--target` 时退回
   宿主工具链链接，基线跟着 runner 走（ubuntu-24.04 下等于没修，端到端
   实测会钉出 bookworm 级 2.34 而非 zig 的 2.30）。Rust 代码仍是 runner
   原生架构，不是伪装 target。插件 `scripts/build.sh` 的 `~/.cargo/bin`
   PATH 前置因此改为条件式，避免把包装器压回去。
2. 导出 `CGO_ENABLED=0`：ldap/kafka 的 Go sidecar 经 CLI 内部 `go build`
   构建时保持静态链接（cgo 构建会钉死 `GLIBC_2.34+`），与 deploy/web
   镜像内的打包方式一致。

`shared/release/validate_artifact_set.py` 会解包每个 linux candidate，
校验其 ELF sidecar 的最高 GLIBC 符号版本 ≤ 2.31（留出余量；静态 Go 产物
无动态版本要求，直接放行）。基线上调必须是有意识的决策。

本地复现构建效果（以 arm64 为例）：

```bash
pip3 install ziglang && cargo install cargo-zigbuild
rustup target add aarch64-unknown-linux-gnu
cd ssh/backend && cargo zigbuild --release --target aarch64-unknown-linux-gnu
# 在 debian:bookworm-slim 容器里运行产物，应正常进入 stdio 主循环
```

### GitHub Release 发布

四个 `release-*.yml` 继续调用 DBX 官方
`t8y2/dbx/.github/workflows/plugin-release-reusable.yml@plugin-sdk-v1`。
该 workflow 同样使用五平台矩阵，并把候选包上传到对应 GitHub Release。

候选包是 unsigned candidate，不应在本仓库 CI 中放入签名私钥。GitHub Release
发布后由 DBX Store 自动同步完成签名与目录发布。

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
