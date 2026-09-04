---
name: dbx-files-dev
description: DBX Files 插件（io.dbx.files，Rust + Apache OpenDAL sidecar）的开发入口：实施文档、协议形态、多协议引擎与脚本。当用户在 files/ 目录下开发、调试文件浏览/传输/归档能力、跑 files smoke 或排查 OpenDAL 服务映射 / 二进制传输通道问题时使用。
---

# DBX Files 插件（io.dbx.files）开发入口

Rust sidecar（dbx-plugin-sdk，`stdio-framed` + **二进制通道**，与 ssh 插件
同栈）。统一 OpenDAL 引擎访问 fs/s3(MinIO)/webdav/ftp/sftp + 已编译服务透传；
能力：浏览/读写/复制/移动/删除、异步 job 大文件传输（进度 + 取消）、目录同步、
归档（压缩/解压）、预览、公开链接（presign）。

- **唯一工作来源**：`files/docs/IMPL_PLAN_DBX_FILES.zh-CN.md`（v3 决策、迁移映射、
  方法契约含二进制传输槽、异步 job 设计、能力缺口表 §5.5、任务表 F2/F3/F4）；
  进度记 `files/docs/PROGRESS-*.zh-CN.md`。
- 通用规范（环境/硬性约定/故障速查）：skill `dbx-plugin-dev`；宿主验证：skill `dbx-host-e2e`。
- SDK 引用照 ssh 插件 `backend/Cargo.toml` 的 `[patch.crates-io]` 模式指向
  `host/` 子仓库（`../../host/plugins/sdk/rust/dbx-plugin-sdk`）的 Rust SDK。

## 脚本与测试

```bash
scripts/build.sh             # 前端三件套(frozen-lockfile) + cargo release + dbx-plugin package
scripts/test.sh              # 全套：cargo test → 前端三件套 → 构建 → 打包 → framed smoke
scripts/smoke_test.py        # 无 UI 全链路冒烟（fs + memory 段；MinIO/sftp 容器段按 env 自动 SKIP）
scripts/perf_files_test.py   # 传输性能基线
scripts/sidecar_client.py    # stdio-framed 协议客户端（与 ssh 插件同款，勿手搓帧）
```

- 容器段冒烟：`scripts/container_smoke.sh` 一键起全部测试容器（MinIO/s3、
  OpenSSH/sftp+sftp-native 密码段、Samba/smb、mod_dav/webdav、pyftpdlib/ftp，
  凭据运行时随机）跑满七协议冒烟；单容器段调试可按 smoke_test.py 头注释设
  `DBX_FILES_*` 环境变量，缺省相应段 SKIP。
- OpenDAL 服务映射与危险路径判定在前端 `src/lib/opendalServices.ts` /
  `dangerousPaths.ts`，改动须同步对应 `*.spec.ts`。

## 协议要点

- 5 字节帧头 `[kind:u8][len:u32 BE]` + payload；binary 帧内
  `[u16 channel_len][channel][data]`；上传/下载块带 8 字节 BE 前缀
  （sequence/offset）——细节全封装在 `scripts/sidecar_client.py`。
- 传输走异步 job：`transfer/*` 方法族，job id + 进度事件 + 取消；复用
  ssh 插件 `sftp/upload|download/*` 帧语义。
- 凭据 Builder 全内存（无 obscure 层/无配置文件/不进环境变量），secret 由宿主 binding 管理。

## 前端 binary 事件（files/download/ 分块流）

宿主桥 b15281024 起 binary 事件为 `{ channel, data: Uint8Array }`（旧桥为
`dataBase64`）。`App.vue` 的 `handleBinary` 必须经
**`shared/frontend/binaryEvent.ts`**（公共适配层，勿在插件内另抄）的
`bridgeBinaryBytes` 双形状归一化；`env.d.ts` 两字段 optional；`lib/mockHost.ts`
镜像真实桥当前形状。`lib/binaryEvent.spec.ts` 保留为引用 shared 模块的薄
spec（证明本插件工具链下解析/打包/行为成立）。背景与故障特征见 skill
`dbx-plugin-dev` 的"宿主契约监察"与故障速查。
