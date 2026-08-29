# B 路（B-HOST-E2E）宿主端到端安装验证进度

> 日期：2026-08-29。范围：三插件（ssh-sftp / files / ldap）在测试宿主 DBX.app
> 上的端到端安装验证——出包 → 官方 PluginPackageInstaller 安装（隔离
> app-data）→ 对安装副本双冒烟 → 工作台可达性截图。
> 本文件为 B 路主交付；不改三插件仓源码，未做任何 git commit/push。

## 1. 测试宿主

- 复用既有构建：`/Users/Jinpy/btroot/dbx-plugin-host-worktree/target/debug/bundle/macos/DBX.app`
  （2026-08-28 15:19 框架分支构建，含本地补丁 46cac99/1a7d760），本次未重建。
- 以 `DBX_DATA_DIR=shared/host-e2e/app-data` 启动（隔离数据目录，`dbx.db-wal`
  mtime 确认宿主进程实际写入该目录）；未触碰 `/Applications/DBX.app` 与用户
  正式 DBX 数据。
- 安装器：`dbx-plugin-host-worktree/target/release/examples/install_plugin`
  （官方 PluginPackageInstaller，校验 sha256 + 兼容性），沿用既有
  `shared/host-e2e/install.sh`（plugin-agnostic 版本，已内置 installer
  构建缓存坑的删除重编逻辑；本次未触发重编，一次通过）。

## 2. 三插件出包（全部重打）

三仓源码均新于旧 dist 包（ssh-sftp: ssh.rs/model.rs/App.vue 等；files:
transfers.rs 及前端多文件；ldap: 前端多文件），故全部重新打包。
前端 ui/ 与 sidecar release 二进制均为最新（源码 mtime 早于产物），打包前无需重编。

| 插件 | 包 | sha256 |
| --- | --- | --- |
| ssh-sftp 0.2.2 | `ssh-sftp/dist/io.dbx.ssh-sftp-0.2.2-darwin-arm64.dbxp` | `a67bb683a2db1ee67e37ae8b6d96447a44fe160fb07e55b459289a450edf347e` |
| files 0.1.0 | `files/dist/io.dbx.files-0.1.0-darwin-arm64.dbxp` | `69de194ad402bec2f0b2d23bcbc8982e8b669027d27105fec41ac59b0d24ba7d` |
| ldap 0.1.0 | `ldap/dist/io.dbx.ldap-0.1.0-darwin-arm64.dbxp` | `bd3437b2bcb2826c35b16f7865dc71cfb10c629e929eade202827bc60d3e60bd` |

新坑记录（ldap Go 打包）：npm CLI 包装脚本 `bin/dbx-plugin.js` 在
`DBX_PLUGIN_SDK_ROOT` 未设时**总是**注入捆绑 sdk-root；CLI 的 Go 后端构建
（`plugins/sdk/cli/src/lib.rs build_go_backend`）据此写 `go.work`（`go 1.22`），
与 ldap 模块 `go 1.24.0` 冲突，报「module . listed in go.work file requires
go >= 1.24.0」。绕过方式：直接调用原生二进制
`@dbx-app/plugin-cli/node_modules/@dbx-app/plugin-cli-darwin-arm64/bin/dbx-plugin`
并 `env -u DBX_PLUGIN_SDK_ROOT`（go.mod 的 replace 已指向宿主 worktree SDK，
无需 workspace）。此坑值得反馈给 CLI 或调整 SDK 模板 go 版本。

## 3. 安装记录（隔离 app-data）

命令：`sh shared/host-e2e/install.sh <plugin-dir> --reinstall`（默认隔离
`shared/host-e2e/app-data`，不重启/不杀任何 DBX）。日志：`logs/install-*.log`。

| 插件 | 安装结果 | activation（sequence=1，camelCase） |
| --- | --- | --- |
| io.dbx.ssh-sftp | v0.2.2 ✓ | packageSha256 `a67bb683…`，activatedAt 2026-08-28T20:39:59Z |
| io.dbx.files | v0.1.0 ✓ | packageSha256 `69de194a…`，activatedAt 2026-08-28T20:40:07Z |
| io.dbx.ldap | v0.1.0 ✓ | packageSha256 `bd3437b2…`，activatedAt 2026-08-28T20:40:08Z |

三条 activation 的 packageSha256 与第 2 节新包 sha256 一致，确认安装的就是
本次测的包。安装结构符合契约：`plugins/<id>/versions/<ver>/` +
`activations/<20位序号>-<uuid>.json`。

## 4. 双冒烟（对安装副本二进制）

均以 `DBX_PLUGIN_SIDECAR=app-data/plugins/<id>/versions/<ver>/bin/darwin-arm64/<binary>`
驱动，日志在 `logs/`。

| 插件 | 脚本 | 结果 |
| --- | --- | --- |
| ssh-sftp | `smoke_test.py`（连接→挑战→PTY→SFTP→关闭，真机 dbx-ssh-test 容器） | **PASS** 全链路 1.2s（logs/smoke-ssh-sftp.log） |
| ssh-sftp | `smoke_fs_test.py`（sudo/sftp_ext/keys 17 用例） | **PASS 17 / SKIP 0 / FAIL 0**（logs/smoke-ssh-fs.log） |
| ldap | `smoke_container.py`（起 bitnami slapd 容器→wait_ready→smoke_test.py→down -v，密码仅走环境变量） | **PASS 10 / FAIL 0 / SKIP 0**（S1-S10 全过，logs/smoke-ldap-installed.log） |
| files | `smoke_test.py` | **PASS 32 / SKIP 2 / FAIL 0**（SKIP=S3/SFTP 容器段需 env，允许语义；logs/smoke-files.log） |

## 5. 工作台可达性（截图 screenshots/）

宿主启动正常（`[STARTUP] plugins registered`），截图验证链：

| 截图 | 验证点 | 结论 |
| --- | --- | --- |
| 01-launch.png | 测试宿主主界面，隔离数据启动 | ✓ |
| 02-plugin-center.png | 插件中心「插件商店」tab 打开 | ✓（商店 tab 因远端 catalog 缺 `signature` 字段报错，与本地安装无关） |
| 03-plugin-center.png | 「已安装」tab：**三插件全部可见**——DBX 文件 v0.1.0、DBX LDAP v0.1.0、DBX SSH 与 SFTP v0.2.2，均标「兼容」 | ✓（对应 M0-T3 验收） |
| 04-new-connection.png | 新建连接对话框「插件」分类列出 存储/LDAP 服务器/SSH 服务器 | ✓ |
| 05-ldap-form.png | **ldap url 形态**：`LDAP URL` 字段（ldap://127.0.0.1:389 占位），说明「ldap/ldaps URL. Network traffic dials the DBX transport endpoint」；认证方式下拉（Simple）、StartTLS/TLS 校验/TLS CA 路径/TLS 服务器名 | ✓（M0-T6-② url 绑定 host 形态） |
| 06/07-files-*.png | **files protocol 联动**：协议=本地文件系统时仅 根路径/锁定/只读/允许删除/超时；切 FTP 后出现 端点（S3/WebDAV/FTP: full URL，SFTP: OpenSSH style user@host）、用户、密码 字段——visible_when 联动生效 | ✓（M0-T6-②） |
| 08-ssh-form.png | ssh 表单：主机/端口/用户名、**认证方式下拉**（默认「密码」，说明含密码/私钥/SSH Agent/无认证）、连接超时、Keepalive、Quick Sudo（自动提权）toggle、sudo 密码覆盖 | ✓ |

GUI 自动化限制：认证方式/协议的原生 select 下拉**展开态**菜单截图未捕获
（NSMenu 层不受 System Events 截屏时序控制），但控件可聚焦可交互、联动结果
已通过表单字段变化证实。人工复核步骤：

1. 打开测试宿主（`DBX_DATA_DIR=shared/host-e2e/app-data open -n …/DBX.app`）
2. 新建连接 → 插件 → SSH 服务器 → 下一步 → 点「认证方式」下拉，应见
   密码/私钥/SSH Agent/无认证；选「私钥」应出现本机密钥下拉
   （`list_local_ssh_keys`）并隐藏密码字段。
3. 插件中心 → 已安装 → 逐个查看详情页（能力/工作台清单）。

## 6. 结论与遗留

- **B 路验收达成**：宿主构建复用 ✓、三插件新包出包 ✓、官方安装器隔离安装 ✓
  （activation sha256 对账一致）、双冒烟全绿（0 FAIL）✓、工作台三插件可见 +
  三表单渲染/联动 ✓。
- 遗留/后续：
  1. ldap Go 打包的 CLI `go.work` 坑（§2）——建议 SDK CLI 模板 go 版本跟进或
     写入 docs/FRAMEWORK_FEEDBACK.zh-CN.md；
  2. 插件商店远端 catalog `missing field "signature"`（宿主商店 tab 报错），
     与本地安装链路无关，属 marketplace/catalog 契约问题；
  3. files 冒烟 S3/SFTP 容器段保持 SKIP（需 `DBX_FILES_S3_*` /
     `DBX_FILES_SFTP_*` env），如需全量可参照 ssh 容器方式补 env；
  4. 下拉展开态截图为 GUI 自动化限制，已给人工复核清单（§5）；
  5. 本轮全程未 commit；三仓 dist/ 内新包为工作区产物，由主会话决定入库时机。
