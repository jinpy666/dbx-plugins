---
name: dbx-web-docker
description: 把四插件打进 DBX web Docker 镜像并发布到服务器的全流程：deploy/web 的镜像构建、插件自动安装机制、服务器部署与排障。当用户要求 docker/web 方式发版插件、部署到服务器 web 访问、或排查镜像构建/容器内插件安装问题时使用。
---

# DBX web Docker 发版（deploy/web）

把工作区四插件打进 DBX web 官方镜像模式（参考宿主 `deploy/Dockerfile`），
镜像内置 dbx-web + 四插件 Linux .dbxp，容器首次启动自动安装插件，
浏览器访问 `:4224` 即用。

## 机制（先懂再改）

- web 模式插件存储 = `$DBX_DATA_DIR/plugins`（官方镜像默认 `/app/data`），
  加载语义与桌面端一致（`versions/<ver>/` + `activations/`）。
- 插件 .dbxp **必须在 Linux 平台打包**：打包时官方 CLI 会把 manifest 的
  `executable` 改写为 `bin/<linux-arch>/...`，且 CLI 拒绝跨平台打包
  （`Native plugin target ... does not match build host`）。因此镜像构建
  的架构 = 打包产物的架构；amd64 服务器镜像需 `--platform linux/amd64`
  （QEMU 模拟，慢）或直接在 amd64 机器上构建。
- Rust sidecar 经 PATH 包装器把 CLI 裸调的 `cargo build` 转成
  `cargo zigbuild --target ...` 交叉编译；Go 插件（ldap、kafka）走
  `GOOS/GOARCH` 原生交叉，无需包装。
- 构建 = 两个上下文：`.`（本工作区）+ 命名上下文 `host=`（精简后的宿主
  子仓库 `host/`，`scripts/release-docker.sh` 会 rsync 剔除 target/node_modules/.git）。
  插件 `[patch.crates-io]` 的相对路径 `../../host`
  依赖镜像内 `/build/{ws/<p>,host}` 布局（与本地结构一致）。
- 容器 entrypoint（`deploy/web/entrypoint.sh`）读 `/app/plugins-builtin/
  plugins.tsv`（id/version/文件），版本目录不存在才调 `install_plugin`
  安装——幂等，升级镜像即升级插件，数据卷里已有数据不受影响。

## 常用命令

```bash
scripts/release-docker.sh                          # 本机架构镜像 + 冒烟验证 + 产物清理
scripts/release-docker.sh --skip-host-sync         # 宿主 worktree 没动过时复用上下文
scripts/release-docker.sh --platform linux/amd64   # 服务器为 amd64 时
scripts/release-docker.sh --tag repo/dbx-plugins:v1 --push   # 推送镜像仓库
scripts/release-docker.sh --no-clean               # 跳过清理步骤
```

脚本末尾自带冒烟：起临时容器（DBX_DISABLE_PASSWORD=1 + 临时卷），检查
plugins.tsv、entrypoint 安装结果、HTTP 200，随后清理。每次 release 结束
自动清理旧产物释放空间（`--no-clean` 跳过）：本流水线冒烟残留容器/卷
（含被杀运行遗留的）、悬空镜像、同名仓库旧镜像（`GC_DOCKER_KEEP=3` 默认
保留最新 3 个 tag）、buildx 缓存超过 `GC_BUILDER_KEEP=8g` 的部分；不触碰
其他项目的镜像和卷。

## 服务器部署

```bash
# 镜像导入/推送后：
docker compose -f deploy/web/docker-compose.yml up -d
# 浏览器 http://<host>:4224，用 DBX_PASSWORD 登录；插件中心应看到四插件。
```

- 暴露到公网前必须改 `DBX_PASSWORD`；数据在 `dbx-data` 卷。
- 验证插件：插件中心列表 → 新建连接（SSH/LDAP/Files/Kafka 表单）→ 工作台可用。
  web 模式没有宿主 fileTransfer，上传走浏览器 File API 兜底（插件已内置）。

## 排障速查

| 症状 | 处理 |
| --- | --- |
| COPY entrypoint.sh not found | Dockerfile 上下文是仓库根，路径须写 `deploy/web/entrypoint.sh` |
| SKIP_HOST_SYNC: unbound variable | 脚本变量名统一 SKIP_HOST_SYNC |
| patch 解析到 /build 外 | 布局必须镜像本地结构：插件在 `/build/ws/<p>/`，宿主树在 `/build/host`（`../../host` 从 `<p>/backend` 上溯两级到 `/build`） |
| CLI 报 unexpected argument 'build' | zigbuild 不接受 `build` 子参数：包装器先 `[ "$1" = build ] && shift` |
| Rust 编译成功但 CLI 找不到二进制 | 带 `--target` 会把产物放进 `target/<triple>/release/`，CLI 只认 `target/release/`——不注入 `--target`（RUN 进程即目标平台，原生构建即可） |
| `GET /` 404 插件 API 正常 | Stage 4 漏了 `COPY --from=frontend /app/dist /app/static` |
| 插件中心看不到插件 | 进容器查 `$DBX_DATA_DIR/plugins/<id>/versions/`；entrypoint 日志有无 install failed |
| 插件 sidecar 起不来 | 确认 .dbxp 内 bin/<arch> 与镜像架构一致（file 命令看 ELF 架构） |
| 构建中 go.work/go 版本冲突 | 镜像内用 golang:1.24 toolchain（Dockerfile 已 COPY --from=golang） |
| ldap 打包 FAIL | CGO_ENABLED=0 必须设置（Dockerfile 已带） |
| pip 装 ziglang 超时 | 偶发网络问题，已加 `--retries 5 --timeout 120`；重跑即可 |
