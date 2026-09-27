# syntax=docker/dockerfile:1
# =============================================================================
# DBX web 镜像（含本工作区四插件 io.dbx.ssh / io.dbx.ldap / io.dbx.files /
# io.dbx.kafka；插件清单硬编码于下方 Stage 2/3 的 COPY 与 for 循环）
#
# 参考官方 deploy/Dockerfile 的构建方式（frontend + zigbuild 交叉 backend +
# debian-slim 运行时），在其上增加插件层：
#   - Linux 平台的插件 sidecar（Rust 经 cargo-zigbuild 交叉 / Go 经 GOARCH）
#     由官方 dbx-plugin CLI 在容器内打包为 .dbxp。打包时 CLI 会把 manifest 的
#     executable 改写为 bin/<linux-arch>/...，因此必须以目标平台打包，
#     不能复用 macOS 产物。
#   - .dbxp 烤进镜像 /app/plugins-builtin，容器首次启动时 entrypoint 调官方
#     PluginPackageInstaller（install_plugin）装入 $DBX_DATA_DIR/plugins：
#     兼容数据卷挂载，版本目录已存在则跳过，升级镜像即升级插件。
#
# Build（两个构建上下文，见 scripts/release-docker.sh）：
#   docker buildx build \
#     --build-context host=/tmp/dbx-docker-host \
#     -f deploy/web/Dockerfile -t dbx-web-plugins:latest .
# =============================================================================

# ---- Stage 1: 宿主 web 前端（与官方 Dockerfile 相同） ------------------------
FROM --platform=$BUILDPLATFORM node:22-slim AS frontend
WORKDIR /app
RUN npm i -g pnpm
# Keep the workspace package available because Vite resolves it directly from source.
COPY --from=host package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY --from=host packages/mongo-shell/ packages/mongo-shell/
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY --from=host apps/desktop/ apps/desktop/
COPY --from=host scripts/ scripts/
COPY --from=host plugins/ plugins/
COPY --from=host crates/dbx-core/assets/database-drivers.manifest.json crates/dbx-core/assets/database-drivers.manifest.json
RUN pnpm build

# ---- Stage 2: 插件前端（三插件自包含 UI，产出 <plugin>/ui/index.html） --------
FROM --platform=$BUILDPLATFORM node:22-slim AS plugin-ui
WORKDIR /build
COPY ssh/frontend/ ssh/frontend/
COPY files/frontend/ files/frontend/
COPY ldap/frontend/ ldap/frontend/
COPY kafka/frontend/ kafka/frontend/
# 插件前端引用各插件仓内 shared/frontend（相对 frontend 的 ../..），须一并拷入；
# 工作区根 shared/ 是同步源，不是插件构建的解析目标
COPY ssh/shared/ ssh/shared/
COPY files/shared/ files/shared/
COPY ldap/shared/ ldap/shared/
COPY kafka/shared/ kafka/shared/
# 插件前端 ?raw 引用插件根 assets/（如 PluginLogo 的 plugin.svg）
COPY ssh/assets/ ssh/assets/
COPY files/assets/ files/assets/
COPY ldap/assets/ ldap/assets/
COPY kafka/assets/ kafka/assets/
# pnpm store 走 cache mount：四个前端依赖在层失效（源码变更）后不必全量重拉，
# 与 Stage 1 的宿主前端同一份 store。pnpm>=11 自管版本会改用 @pnpm/exe 独立
# 二进制，依赖 libatomic（node:22-slim 未带），须先装上。
RUN apt-get update && apt-get install -y --no-install-recommends libatomic1 \
    && rm -rf /var/lib/apt/lists/* \
    && npm i -g pnpm \
    && for p in ssh files ldap kafka; do \
         cd "/build/$p/frontend" \
         && (pnpm install --frozen-lockfile || pnpm install) \
         && pnpm build || exit 1; \
       done \
    && mkdir -p /out/overlay \
    && for p in ssh files ldap kafka; do mkdir -p "/out/overlay/$p" && cp -R "/build/$p/ui" "/out/overlay/$p/ui"; done

# ---- Stage 3: 交叉编译 dbx-web/install_plugin + 打包 Linux .dbxp --------------
FROM --platform=$BUILDPLATFORM rust:1-bookworm AS backend
ARG TARGETARCH
WORKDIR /build/ws

# 工具链层前置：不依赖任何源码 COPY，源码变更不会触发 apt / ziglang /
# cargo-zigbuild 的全量重装（这一层冷装要好几分钟）。
RUN dpkg --add-architecture arm64 \
    && apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake pkg-config perl python3-pip gcc-aarch64-linux-gnu \
    libfontconfig-dev:arm64 libfreetype-dev:arm64 \
    && pip3 install --break-system-packages ziglang \
    && cargo install cargo-zigbuild \
    && rustup target add x86_64-unknown-linux-gnu aarch64-unknown-linux-gnu \
    && rm -rf /var/lib/apt/lists/*
# Go >= 1.24（ldap 插件 go.mod 要求；bookworm apt 的 go 太老）
COPY --from=golang:1.24-bookworm /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

# cargo 包装器：官方 CLI 打包时裸调 `cargo build --release ...`，转给 zigbuild
# 执行（剥掉首参 build）。--target 由 CLI 依据 DBX_RUST_TARGET 传入。
# 包装器必须直调真实 cargo，避免递归。
RUN mkdir -p /wrappers \
    && printf '#!/bin/sh\n[ "$1" = "build" ] && shift\nexec /usr/local/cargo/bin/cargo zigbuild "$@"\n' > /wrappers/cargo \
    && chmod +x /wrappers/cargo

RUN case "$TARGETARCH" in \
      amd64) echo x86_64-unknown-linux-gnu > /tmp/rust_target; echo amd64 > /tmp/goarch ;; \
      arm64) echo aarch64-unknown-linux-gnu > /tmp/rust_target; echo arm64 > /tmp/goarch ;; \
      *) echo "Unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac

# 宿主子仓库（release-docker.sh 已剔除 target/node_modules/.git）与插件源码。
# 布局与本地结构一致：插件在 /build/ws/<p>/，宿主树在 /build/host
# ——插件 [patch.crates-io] 的相对路径 ../../host 从 /build/ws/<p>/backend
# 上溯两级正好落在 /build。
COPY --from=host . /build/host/
COPY ssh/ ssh/
COPY files/ files/
COPY ldap/ ldap/
COPY kafka/ kafka/
# 覆盖插件 UI 构建产物（frontend build 生成 ui/index.html）
COPY --from=plugin-ui /out/overlay/ ./

# ---- dbx-web + install_plugin（交叉编译，产物随镜像架构） --------------------
# dbx-core 的 build.rs 需要 plugins/dialects + plugins/connection-types（已随 host 树拷入）。
# cargo registry/git 与 target 走 cache mount：源码小幅变更时只重编改动的 crate
# 而不是整棵依赖树；target 内产物都在 <triple>/ 子目录下，多架构共享同一份缓存
# 不会串（cargo 按指纹区分）。
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=cargo-registry \
    --mount=type=cache,target=/usr/local/cargo/git,id=cargo-git \
    --mount=type=cache,target=/build/host/target,id=target-host \
    cd /build/host \
    && RUST_TARGET="$(cat /tmp/rust_target)" \
    && ARCH_PREFIX="$(echo "$RUST_TARGET" | cut -d- -f1)-linux-gnu" \
    && LIBRARY_PATH="/usr/lib/$ARCH_PREFIX" \
    PKG_CONFIG_PATH="/usr/lib/$ARCH_PREFIX/pkgconfig" \
    PKG_CONFIG_SYSROOT_DIR="/" \
    RUSTFLAGS="-L native=/usr/lib/$ARCH_PREFIX" \
    cargo zigbuild --release -p dbx-web --target "$RUST_TARGET" \
    && cargo zigbuild --release -p dbx-core --example install_plugin --target "$RUST_TARGET" \
    && mkdir -p "/out/linux/$TARGETARCH" \
    && cp "target/$RUST_TARGET/release/dbx-web" "/out/linux/$TARGETARCH/" \
    && cp "target/$RUST_TARGET/release/examples/install_plugin" "/out/linux/$TARGETARCH/"

# ---- 官方 dbx-plugin CLI（构建期使用，本机架构即可） --------------------------
# target 走 cache mount 提速，但 cache mount 只在声明它的 RUN 内可见——
# 构建完立即把二进制拷到普通层路径 /out/cli，供后面的打包 RUN 引用。
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=cargo-registry \
    --mount=type=cache,target=/usr/local/cargo/git,id=cargo-git \
    --mount=type=cache,target=/build/host/plugins/sdk/cli/target,id=target-cli \
    cd /build/host/plugins/sdk/cli \
    && cargo build --release \
    && mkdir -p /out/cli && cp target/release/dbx-plugin /out/cli/

# ---- 打包三插件为 Linux .dbxp + 生成 entrypoint 安装清单 ----------------------
# ssh/files 的 Rust sidecar：与本地 build.sh 同款技巧——把 CLI 打包时的内部
# cargo build 指到插件 backend/target 的 cache mount，跨次构建增量复用（CLI
# 会复用并保留该目录；没有它 CLI 用完即删的 dist/.build-rust-<target> 等于每次
# 全量重编）。registry/git 与上面各 RUN 同 id，同 builder 内跨层共享下载缓存。
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=cargo-registry \
    --mount=type=cache,target=/usr/local/cargo/git,id=cargo-git \
    --mount=type=cache,target=/build/ws/ssh/backend/target,id=target-ssh \
    --mount=type=cache,target=/build/ws/files/backend/target,id=target-files \
    RUST_TARGET="$(cat /tmp/rust_target)" \
    && GOARCH="$(cat /tmp/goarch)" \
    && CLI=/out/cli/dbx-plugin \
    && mkdir -p /out/plugins /out/linux \
    # files 的 package.include 含 bin/（本地空目录，git 不跟踪空目录，检出即缺失）
    && mkdir -p /build/ws/files/bin \
    && for p in ssh files; do \
         cd "/build/ws/$p" \
         && env -u DBX_PLUGIN_SDK_ROOT PATH="/wrappers:$PATH" DBX_RUST_TARGET="$RUST_TARGET" \
              CARGO_TARGET_DIR="/build/ws/$p/backend/target" \
              NO_COLOR=1 "$CLI" package . \
         && cp dist/*.dbxp /out/plugins/ || exit 1; \
       done \
    && for p in ldap kafka; do \
         cd "/build/ws/$p" \
         && env -u DBX_PLUGIN_SDK_ROOT GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 \
              NO_COLOR=1 "$CLI" package . \
         && cp dist/*.dbxp /out/plugins/ || exit 1; \
       done \
    && : > /out/plugins.tsv \
    && for p in ssh ldap files kafka; do \
         id="$(python3 -c "import json;print(json.load(open('/build/ws/$p/manifest.json'))['id'])")"; \
         ver="$(python3 -c "import json;print(json.load(open('/build/ws/$p/manifest.json'))['version'])")"; \
         f="$(ls /out/plugins/ | grep "^$id-")"; \
         printf '%s\t%s\t%s\n' "$id" "$ver" "$f" >> /out/plugins.tsv; \
       done \
    && cat /out/plugins.tsv \
    && python3 -c "import tomllib;print(tomllib.load(open('/build/host/crates/dbx-web/Cargo.toml','rb'))['package']['version'])" > /out/VERSION

# ---- Stage 4: 运行时（与官方镜像相同的系统依赖 + 插件层） ---------------------
FROM debian:bookworm-slim
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    fontconfig \
    fonts-dejavu-core \
    libfreetype6 \
    libssl3 \
    openjdk-17-jre-headless \
    && rm -rf /var/lib/apt/lists/*
COPY --from=backend /out/linux/${TARGETARCH}/dbx-web /usr/local/bin/dbx-web
COPY --from=backend /out/linux/${TARGETARCH}/install_plugin /usr/local/bin/install_plugin
COPY --from=backend /out/plugins/ /app/plugins-builtin/
COPY --from=backend /out/plugins.tsv /app/plugins-builtin/plugins.tsv
# web 前端静态资源（Stage 1 产物，官方镜像同款）
COPY --from=frontend /app/dist /app/static
COPY --from=backend /out/VERSION /app/VERSION
COPY deploy/web/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
ENV DBX_STATIC_DIR=/app/static
ENV DBX_DATA_DIR=/app/data
ENV DBX_JAVA_BIN=/usr/bin/java
EXPOSE 4224
ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["dbx-web"]
