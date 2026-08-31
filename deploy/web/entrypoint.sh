#!/bin/sh
# DBX web 镜像入口：首次启动（或镜像升级出新版本）时，把烤进镜像的插件
# .dbxp 通过官方 PluginPackageInstaller 装入 $DBX_DATA_DIR/plugins，再启动 dbx-web。
# 版本目录已存在则跳过（幂等）；数据卷挂载 / 升级镜像均适用。
set -e

DATA_DIR="${DBX_DATA_DIR:-/app/data}"
STORE="$DATA_DIR/plugins"
APP_VERSION="$(cat /app/VERSION 2>/dev/null || echo 0.0.0)"

if [ -f /app/plugins-builtin/plugins.tsv ]; then
  while IFS="$(printf '\t')" read -r id version file; do
    [ -n "$id" ] || continue
    if [ -d "$STORE/$id/versions/$version" ]; then
      echo "[entrypoint] plugin $id v$version already installed, skip"
    elif [ -f "/app/plugins-builtin/$file" ]; then
      echo "[entrypoint] installing bundled plugin $id v$version"
      mkdir -p "$STORE"
      if ! install_plugin "$STORE" "/app/plugins-builtin/$file" "$APP_VERSION"; then
        echo "[entrypoint] WARN: install failed for $id v$version" >&2
      fi
    fi
  done < /app/plugins-builtin/plugins.tsv
fi

exec "$@"
