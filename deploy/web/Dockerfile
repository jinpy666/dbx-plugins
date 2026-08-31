# syntax=docker/dockerfile:1
# =============================================================================
# DBX web 镜像（含本工作区三插件 io.dbx.ssh / io.dbx.ldap / io.dbx.files）
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
RUN npm i -g pnpm \
    && for p in ssh files ldap; do \
         cd "/build/$p/frontend" \
         && (pnpm install --frozen-lockfile || pnpm install) \
         && pnpm build || exit 1; \
       done \
    && mkdir -p /out/overlay \
    && for p in ssh files ldap; do mkdir -p "/out/overlay/$p" && cp -R "/build/$p/ui" "/out/overlay/$p/ui"; done

# ---- Stage 3: 交叉编译 dbx-web/install_plugin + 打包 Linux .dbxp --------------
FROM --platform=$BUILDPLATFORM rust:1-bookworm AS backend
ARG TARGETARCH
WORKDIR /build/ws

# 宿主 worktree（release-docker.sh 已剔除 target/node_modules/.git）与插件源码。
# 布局镜像本地结构：插件在 /build/ws/<p>/，宿主树在 /build/dbx-plugin-host-worktree
# ——插件 [patch.crates-io] 的相对路径 ../../../dbx-plugin-host-worktree 从
# /build/ws/<p>/backend 上溯三级正好落在 /build。
COPY --from=host . /build/dbx-plugin-host-worktree/
COPY ssh/ ssh/
COPY files/ files/
COPY ldap/ ldap/
# 覆盖插件 UI 构建产物（frontend build 生成 ui/index.html）
COPY --from=plugin-ui /out/overlay/ ./

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
# 执行（剥掉首参 build）。不注入 --target：RUN 进程本身就跑在目标平台
# （原生或 QEMU），zigbuild 无 --target 即原生构建，产物落 CLI 期望的
# target/release/（带 --target 会多一层 <triple>/ 目录，CLI 找不到）。
# 包装器必须直调真实 cargo，避免递归。
RUN mkdir -p /wrappers \
    && printf '#!/bin/sh\n[ "$1" = "build" ] && shift\nexec /usr/local/cargo/bin/cargo zigbuild "$@"\n' > /wrappers/cargo \
    && chmod +x /wrappers/cargo

RUN case "$TARGETARCH" in \
      amd64) echo x86_64-unknown-linux-gnu > /tmp/rust_target; echo amd64 > /tmp/goarch ;; \
      arm64) echo aarch64-unknown-linux-gnu > /tmp/rust_target; echo arm64 > /tmp/goarch ;; \
      *) echo "Unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac

# ---- dbx-web + install_plugin（交叉编译，产物随镜像架构） --------------------
# dbx-core 的 build.rs 需要 plugins/dialects + plugins/connection-types（已随 host 树拷入）。
RUN cd /build/dbx-plugin-host-worktree \
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
RUN cd /build/dbx-plugin-host-worktree/plugins/sdk/cli && cargo build --release

# ---- 打包三插件为 Linux .dbxp + 生成 entrypoint 安装清单 ----------------------
RUN RUST_TARGET="$(cat /tmp/rust_target)" \
    && GOARCH="$(cat /tmp/goarch)" \
    && CLI=/build/dbx-plugin-host-worktree/plugins/sdk/cli/target/release/dbx-plugin \
    && mkdir -p /out/plugins /out/linux \
    && for p in ssh files; do \
         cd "/build/ws/$p" \
         && env -u DBX_PLUGIN_SDK_ROOT PATH="/wrappers:$PATH" DBX_RUST_TARGET="$RUST_TARGET" \
              NO_COLOR=1 "$CLI" package . \
         && cp dist/*.dbxp /out/plugins/ || exit 1; \
       done \
    && cd /build/ws/ldap \
    && env -u DBX_PLUGIN_SDK_ROOT GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 \
         NO_COLOR=1 "$CLI" package . \
    && cp dist/*.dbxp /out/plugins/ \
    && : > /out/plugins.tsv \
    && for p in ssh ldap files; do \
         id="$(python3 -c "import json;print(json.load(open('/build/ws/$p/manifest.json'))['id'])")"; \
         ver="$(python3 -c "import json;print(json.load(open('/build/ws/$p/manifest.json'))['version'])")"; \
         f="$(ls /out/plugins/ | grep "^$id-")"; \
         printf '%s\t%s\t%s\n' "$id" "$ver" "$f" >> /out/plugins.tsv; \
       done \
    && cat /out/plugins.tsv \
    && python3 -c "import tomllib;print(tomllib.load(open('/build/dbx-plugin-host-worktree/crates/dbx-web/Cargo.toml','rb'))['package']['version'])" > /out/VERSION

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
