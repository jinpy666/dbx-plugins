# 方案：tiny-rdm LDAP 与文件(rclone)能力移植为 DBX 独立插件（定稿）

> 状态：**已定稿**（2026-08-28 决策确认）
> 决策记录：采用 DBX 插件标准 sidecar 架构；LDAP sidecar 用 **Go**（复用
> tiny-rdm 领域代码，官方 Go SDK）；**v3 修订：Files sidecar 改 Rust +
> Apache OpenDAL**（弃 rclone+Go，能力缺口显式声明）。
> 参照源：`/Users/Jinpy/GolandProjects/tiny-rdm`（模块名 `mcpctl`，Go + Wails v3）
> 参照架构：ssh-sftp 插件仓 `~/btroot/dbx-plugins/ssh-sftp`（Vue 沙箱前端 + sidecar + `.dbxp` 打包）
> 架构结论：**两个独立插件**；Files 引擎为 **Apache OpenDAL（Rust crate）**，
> stdio-framed + 二进制帧传输，凭据全内存（v3）。

---

## 1. 背景与调研结论

### 1.1 tiny-rdm LDAP 能力现状

- 核心：`backend/services/ldap_service.go`（2094 行）+ `backend/types/ldap.go`，
  库为 `go-ldap/ldap/v3 v3.4.13`，Kerberos 用 `jcmturner/gokrb5/v8 v8.4.4`
  + 自研 `backend/ldapgssapi` GSSAPIClient。
- 对外 19 个方法（Wails 绑定，全部同步请求-响应，**无事件推送**）：
  profile CRUD/test、Search（RFC 4515 过滤器校验 + `SearchWithPaging` 分页）、
  GetEntry、RootDSE、SchemaMetadata（subschemaSubentry 发现 + 解析
  attributeTypes/objectClasses）、AddEntry、ModifyEntry、DeleteEntry、ModifyDN、
  ConnectionStatuses、Disconnect。
- 连接：`ldap://` / `ldaps://` / `ldapi://`(Unix socket) 三 scheme，StartTLS，
  **8 种认证**：anonymous / unauthenticated / simple / kerberos(GSSAPI) / ntlm /
  ntlm_hash / digest_md5 / external；连接池带断线自动重连。
- 安全：读写 DN 白名单（`AllowedBaseDNs` / `AllowedWriteBaseDNs`，`dnWithinBase`）、
  默认屏蔽属性表（userPassword、unicodePwd、objectSid 等 11 项）、ReadOnly、审计；
  密码经 secretRef / 加密存储。
- 前端：`LdapConsolePage.vue`（8543 行）：profile 列表 + DN 树（scope=one 展开、
  远程关键字过滤）+ 搜索表单 + 结果表 + 条目编辑器 + LDIF/CSV 导出、搜索预设。
- 配置存储：SQLite `ldap_profiles.sqlite3`（移植时替换为宿主连接管理，见 §2 D3）。
- MCP：14 个 ldap_* 工具（写工具白名单 + 策略限制 + 敏感属性脱敏）。

### 1.2 tiny-rdm 文件(rclone) 能力现状

- 集成方式（已验证）：**内嵌库，非外部进程**。`backend/fs/manager.go` 导入
  `rclone/backend/all` + `fs` + `librclone/librclone`，`sync.Once` 懒初始化，
  核心调用 `librclone.RPC(method, input)`；`fs.NewFs → obj.Open / f.Put` 直调
  Go API 做流式读写。全库无 `exec.Command`。
- 依赖：`github.com/rclone/rclone v1.73.5`（直接依赖，随包引入 Proton 等间接依赖）。
- 协议→rclone 映射（`buildRemoteConfig`）：local（原生 os.\*，不走 rclone）、
  sftp（host/user/port + pass/key_file/key_use_agent）、ftp、webdav、
  s3/minio（provider/endpoint/region/ak/sk/force_path_style）、
  以及 **rclone 通用透传**（`RcloneBackend` + `RcloneParameters`，覆盖
  drive/dropbox/azureblob/b2/box/mega/onedrive/smb/storj/swift/union 等全部后端）。
- 文件操作：List/ListPaged（内存分页）、Stat、Read/Write(base64)、Mkdir、
  DeleteFile、Rmdir、Purge、CopyFile、MoveFile、Rename、SyncDir、CopyDir、
  UploadFile、DownloadFile、OpenReadStream、About、Size、PublicLink、Chmod(仅 local)、
  远端注册 `RegisterRemote/RemoveRemote`、连接 CRUD/Test/Connect/Disconnect、
  传输记录（内存最近 50 条）。
- 安全要点：**敏感字段禁止走环境变量**（manager.go:126-131），改 `config/create`
  + `obscure:true` 传入；写/删/拷/移/同步/上传/下载过审批 + 审计；密码/SecretKey
  存 secret registry（Keychain/Vault/local vault），DB 只存引用。
- 传输模型（短板）：`runTransfer` **同步阻塞**，进度为事后 0→100% 推导，
  靠前端轮询 `FsListTransfers()`（活跃 2s/空闲 15s）；无并发队列。
- 前端：ToolsFilePage + FileBrowserPane（1296 行）+ 连接面板/对话框（1431 行）
  + 传输面板 + 审计面板 + `rcloneConfig.js`（后端列表与字段模板）。

### 1.3 DBX 插件侧约束（沿用 ssh-sftp 插件仓经验，`dbx-plugins/ssh-sftp`）

- 结构：`manifest.json`（connection-provider / workbench / filesystem-provider
  贡献点 + 字段 + 七语）+ `ui/`（Vue3 沙箱）+ `bin/<platform>/` sidecar，
  `dbx-plugin` CLI 打包 `.dbxp`，目标 Host API 1.0。
- sidecar 传输：`stdio-framed`，5 字节帧头 `[kind:u8][len:u32 BE]` + payload；
  binary 帧带 `[u16 channel_len][channel][data]`；方法名分发
  （`connection/test`、`connection/connect`、`workbench/close`、`filesystem/list`…），
  参数/返回 camelCase，`operationId` 缺失时本地生成兜底。
- 硬性约定：不重复宿主（隧道/代理走 DBX 传输层，sidecar 用 `runtime.host/port`
  拨号）；`engines.host_api >= 1.0.0`，宿主 1.1 特性一律 optional 降级；
  新文案七语全补；写操作过只读/审批防线；新能力 = 单测 + smoke + 对标清单。

---

## 2. 总体决策（已锁定）

### D0 两个独立插件，不是一个"工具箱"插件 ✅

| | 插件 A：DBX LDAP | 插件 B：DBX Files |
|---|---|---|
| 插件 ID | `io.dbx.ldap` | `io.dbx.files` |
| 仓库 | `~/btroot/dbx-plugins/ldap` | `~/btroot/dbx-plugins/files` |
| sidecar 二进制 | `dbx-plugin-ldap` | `dbx-plugin-files` |
| sidecar 语言 | **Go**（官方 Go SDK，stdio-jsonl） | **Rust**（dbx-plugin-sdk，stdio-framed + 二进制帧） |
| 贡献点 | connection-provider + workbench | connection-provider + workbench |
| 体积预估 | ~15-25MB（gokrb5 纯 Go，无 cgo） | ~10-20MB（opendal feature 白名单） |

理由：rclone 内嵌后二进制大、依赖重，与 LDAP 无任何共享运行时；拆开可独立
安装/升级/下架，用户可只装 LDAP。**不集成进 `io.dbx.ssh-sftp`**（已确认）。

### D1 sidecar 语言：LDAP=Go，Files=Rust（v3 修订）✅

- LDAP 侧 Go：复用 tiny-rdm 的 Go 领域代码（`ldap_service.go` /
  `types/ldap.go` / `ldapgssapi/` 整体搬迁，8 种认证零重写），走官方
  Go SDK（stdio-jsonl，M0 文档 §1）。
- Files 侧 Rust（v3）：OpenDAL 的 Go 绑定经 opendal-c + purego/**libffi**
  （运行时依赖系统 libffi，官方状态 "Released and usable" 而非
  production-ready），Rust 核心才是生产路径；且 dbx-plugin-sdk Rust 栈
  与 ssh-sftp 插件同款——stdio-framed + **二进制帧**，大文件传输无需
  base64（Go/jsonl 路线的硬限制随之消除）。
- 两插件协议栈不同，但 lifecycle / 审计 / 方法命名基线一致（M0 文档）。

### D2 Files 引擎：Apache OpenDAL（Rust crate），弃 rclone（v3 修订）✅

- 引擎 = `opendal` crate（0.58.x，Apache-2.0，63 个 services feature），
  编译期白名单启用 fs/s3/webdav/ftp/sftp/gcs/azblob/oss/memory，
  体积预估 ~10-20MB（对比 rclone 内嵌 40-80MB）。
- Operator 全内存构建（Builder kv），凭据不落盘、不进环境变量——
  rclone 时代的 obscure/config-create 红线层整体消失。
- 快捷协议字段 key = OpenDAL 配置键直译；其余服务走 `Operator::via_iter`
  通用透传（等价 rclone-generic）；presign(签名型后端)/capability 运行时探测。
- 能力缺口显式声明：无容量查询（About）、chmod 不适用、目录 sync 自研
  遍历 job——详见 Files 实施文档 §5.5。

### D3 连接配置归宿主，插件不自带 SQLite ✅

- tiny-rdm 用 SQLite 存 profile + secrets registry；DBX 里 connection-provider
  字段（含 `binding: secret` 的密码类字段）由宿主连接管理存储，sidecar 每次从
  `connection/connect` 参数拿现值，插件侧**不持久化任何凭据**。
- 插件本地数据目录（JSON）只放非敏感偏好：LDAP 搜索预设/列宽、files 传输历史、
  UI 状态。顺带消灭 `go-sqlite3` 的 cgo 依赖，保证纯 Go 交叉编译。

### D4 特性对齐 tiny-rdm：全量对齐、分期交付 ✅

- 对齐目标见 §2.1 矩阵：LDAP 19 方法 + 8 认证 + 14 MCP 工具、
  Files 22 方法 + 全部 rclone 后端（通用透传）+ 字段模板。
- 分期只决定**顺序**，不永久裁剪：云盘 OAuth 类（drive/dropbox/onedrive 等）
  经 `rclone-generic` 透传即可覆盖（对齐 tiny-rdm 的 `RcloneParameters` 模式），
  精选字段模板逐步补齐。
- 不对齐项（有意为之，DBX 语义替代）：SQLite profile 存储（→宿主连接管理）、
  SSH 隧道/代理传输层与 endpoint rewrite（→DBX 传输层）、审批弹窗
  （`RequireSystemApproval` → 插件内确认 + 审计日志）。

### D5 传输层与既有能力划界 ✅

- LDAP 的 `Transport(CommonTransportConfig)`（SSH 隧道/代理）与 endpoint rewrite
  **不移植**，改用 DBX 传输层语义——sidecar 经 `runtime.host/port` 拨号，
  TLS SNI/SPN 用逻辑主机名校验（对齐 ssh-sftp 插件既有做法）。
- Files 插件的 `sftp` 协议类型与 `io.dbx.ssh-sftp`（filesystem-provider
  scheme=`sftp`）划界：SSH 插件管「SSH 会话内文件工作台」；Files 插件管
  「存储连接」（s3/webdav/ftp/云盘…），其 sftp 类型定位为存储账号通道，
  UI 上注明差异。sftp 拨号同样优先复用 DBX 已有 SSH 连接/传输层。
- filesystem-provider 贡献：首期不做（无 tiny-rdm 对应物，非对齐缺口），
  待宿主 filesystem 契约稳定后 Phase 2 评估。

### 2.1 特性对齐矩阵（tiny-rdm → DBX 插件目标）

| tiny-rdm 能力 | 目标插件 | 对齐方式 | 里程碑 |
|---|---|---|---|
| LDAP 19 个服务方法 | io.dbx.ldap | 全量移植（profile CRUD 转为本地偏好/宿主连接，见 D3） | M1-M3 |
| LDAP 8 种认证（含 GSSAPI/NTLM/Digest-MD5） | io.dbx.ldap | 全量移植（go-ldap + gokrb5 + ldapgssapi 原样） | simple/anonymous M1，其余 M3 |
| RootDSE / Schema 元数据 | io.dbx.ldap | 全量移植 | M1 |
| DN 树/搜索表单/结果表/条目编辑/LDIF/CSV/预设 | io.dbx.ldap | 前端重实现（拆组件），工具函数直搬 | M1-M2 |
| LDAP 安全策略（DN 白名单/屏蔽属性/ReadOnly/审计） | io.dbx.ldap | 代码级移植 | M1 |
| LDAP 14 个 MCP 工具 | io.dbx.ldap | 按 ssh-sftp 插件 mcp.rs 模式重实现 | M4 |
| Files 22 个 Fs 方法 | io.dbx.files | 对齐（About/Chmod 不适用、SyncDir 自研 job，缺口表显式声明） | M2-M3 |
| OpenDAL 服务矩阵（s3/ftp/webdav/sftp/云盘…） | io.dbx.files | 快捷五协议 + via_iter 通用透传（编译期 feature 白名单） | 常用 M2，透传 M2，模板 M3+ |
| OpenDAL 引擎（Rust crate） | io.dbx.files | Operator 内存构建；presign/capability 运行时探测；二进制帧传输槽 | M0 验证 |
| 凭据安全（内存 Builder / 无落盘） | io.dbx.files | 凭据只进 Operator；secret 存储→宿主 secret binding | M2 |
| 传输记录/进度 | io.dbx.files | 对齐功能并升级：同步阻塞 → 异步 job + 进度推送/轮询降级 | M3 |
| 审批（RequireSystemApproval） | 两插件 | 语义对齐，实现换为插件内确认 + 审计 | M2-M3 |
| fs/sftp MCP 侧（fs_save_connection 等） | io.dbx.files | M4 与 LDAP MCP 一并 | M4 |
| SQLite profile 存储 | — | 不对齐（宿主连接管理替代，D3） | — |
| SSH 隧道/代理/endpoint rewrite | — | 不对齐（DBX 传输层替代，D5） | — |

---

## 3. 插件 A：dbx-ldap-plugin

### 3.1 manifest 草案（要点）

- `permissions`: `host.workbench`（工作台 UI）。不需要 `host.binary`。
- connection-provider `io.dbx.ldap.connection`（`database_type: "ldap"`）核心字段：

| key | 类型 | binding | 说明 |
|---|---|---|---|
| display_name | text | name | 连接名 |
| url | text | host | `ldap(s)://host:port`（ldapi:// 视宿主支持度，Phase 2） |
| base_dn | text | config | 默认 BaseDN |
| auth_type | select | config | anonymous / unauthenticated / simple / kerberos / ntlm / ntlm_hash / digest_md5 / external |
| bind_dn / username / domain | text | config | 按 auth_type `visible_when` 联动 |
| bind_password / ntlm_hash | password | secret | 宿主管存储 |
| use_starttls | boolean | config | 与 ldaps 互斥（sidecar 校验兜底） |
| tls_verify / tls_ca_path | text/boolean | config | TLS 校验策略 |
| kerberos_* (credential_type/realm/kdc_host/keytab/ccache) | text/select | config | auth_type=kerberos 时显示 |
| timeout_secs | number | config | 默认 30 |
| read_only | boolean | config | 只读模式 |
| allowed_base_dns / allowed_write_base_dns | textarea | config | DN 白名单，空=不限 |
| blocked_attributes | textarea | config | 默认 11 项屏蔽属性 |

- workbench 贡献 `io.dbx.ldap.workbench`。
- 字段七语照 ssh-sftp 插件格式补全。

### 3.2 Go sidecar 方法表（`<域>/<动作>`，对齐现有命名法）

| sidecar 方法 | 对应 tiny-rdm | 说明 |
|---|---|---|
| `connection/test` | TestProfile | 连前校验（宿主 connection-provider 能力） |
| `connection/connect` | （建池） | 建立并缓存 `*ldap.Conn`，返回 connectionId |
| `connection/disconnect` | Disconnect | 关连接 |
| `ldap/search` | Search | filter 校验/scope/deref/sizeLimit/typesOnly/attrs/分页 |
| `ldap/entry/get` | GetEntry | 单条目属性 |
| `ldap/rootDse` | RootDSE | AllowedBaseDNs 非空时禁用（沿袭） |
| `ldap/schema` | GetSchemaMetadata | 前端 schema 缓存用 |
| `ldap/entry/add` / `ldap/entry/modify` / `ldap/entry/delete` / `ldap/entry/modifyDn` | Add/Modify/Delete/ModifyDN | 写操作 |
| `ldap/connections/statuses` | ConnectionStatuses | 多连接状态面板 |
| `ldap/presets/list|save|remove` | SearchPresetDialog | 搜索预设（本地偏好，替代 profile CRUD） |
| `mcp/tools` / `mcp/call` / `mcp/settings/get|set` | tools_ldap.go 14 工具 | M4，与 ssh-sftp 插件 mcp.rs 同模式 |

- 安全策略随代码移植：`ensureLDAPReadAllowed/ensureLDAPWriteAllowed`（DN 白名单）、
  `BlockedAttributes` 默认表、ReadOnly、写值转义、审计日志（写插件数据目录）。

### 3.3 前端（Vue3 沙箱，从 8543 行拆分）

- 布局：左侧连接列表 + DN 树；右侧 Tab：搜索（表单 + 结果表）、
  条目详情/编辑器、Schema、连接状态。
- 组件拆分：`DnTree.vue`、`SearchForm.vue`、`ResultTable.vue`、
  `EntryEditorDialog.vue`、`DeleteDialog.vue`、`ModifyDnDialog.vue`、
  `SchemaPanel.vue`；工具函数直接搬 `utils/ldif.js`、`ldapFilter.js`（RFC 4515
  转义）、`dn.js`、`ldapExporter.js`（CSV/JSON）。
- 分页与树节点懒加载走 sidecar 同步方法；无事件依赖（tiny-rdm 本身无推送），
  Host API 1.0 即可全覆盖，无降级路径。
- i18n 七语（zh-CN/zh-TW/en/es/it/ja/pt）。

### 3.4 测试

- 单测（纯逻辑，不连网）：filter 校验、DN 白名单 `dnWithinBase`、LDIF 序列化、
  屏蔽属性过滤。
- smoke：复用 `sidecar_client.py`，覆盖 connect → rootDSE → search →
  entry CRUD → modifyDN → disconnect；未注册方法 SKIP。
- 集成容器：搬 tiny-rdm 的 `docker-compose.ldap-test.yml` + `ldap-seed`
  （OpenLDAP 种子数据）；GSSAPI 测试（`ldap-gssapi-test.yml`）M3。
- 验收三件套：单测 + smoke + 对标清单（LDAP 19 方法 × 状态）。

---

## 4. 插件 B：dbx-files-plugin（v3：Rust + Apache OpenDAL）

> 本节为 v3 摘要；完整实施细节（manifest 字段全表、方法契约、传输槽、
> job 设计、里程碑任务）以 `IMPL_PLAN_DBX_FILES.zh-CN.md`（v3）为准。

### 4.1 架构要点

- sidecar：**Rust**（dbx-plugin-sdk，与 ssh-sftp 插件同栈：stdio-framed +
  **二进制通道**），大文件传输复用 `sftp/upload|download/*` 的帧语义
  （256KiB 块 + 8 字节 BE offset），无 base64 主路径。
- 引擎：**opendal crate 0.58.x**（Apache-2.0），feature 白名单
  fs/s3/webdav/ftp/sftp/gcs/azblob/oss/memory；快捷五协议
  （fs/s3/webdav/ftp/sftp）+ `opendal-custom`（service + config JSON 经
  `Operator::via_iter` 透传，等价 v2 的 rclone-generic）。
- 凭据：Builder 全内存构建 Operator，无 obscure/config-create 层、
  无 rclone.conf、不进环境变量；secret 由宿主 binding 管理。
- 传输：异步 job 表（状态机/进度事件/取消/并发信号量/历史 200 条）；
  目录 sync/copyDir 为自研遍历 job（OpenDAL 无服务端 sync）。
- 能力缺口（显式声明）：About 容量查询、Chmod 不适用；publicLink =
  `presign_read`（仅签名型后端）；rename/copy 按后端 capability 运行时
  探测 + job 降级。

### 4.2 安全

- 凭据不落盘/不日志/不回发（redact 统一处理）。
- 写操作过 `read_only` / `allow_delete` / `lock_to_root` 门禁；purge 拒绝
  等于 root 或 `/`（对齐 ssh-sftp removeAll 红线）；危险操作前端确认 +
  `audit.jsonl`。
- 出网校验：HTTP 类后端仅 http/https；host 合法性校验；localhost/环回/
  私有/保留地址拒绝应用于**非用户显式配置来源**的 URL（连接 endpoint 为
  可信输入但独立审计标记）；重定向默认不跟随。
- 修复 tiny-rdm 已知瑕疵的等价物：日志/审计/事件一律 redact。

### 4.3 测试

- 单测含 `memory://` 后端全操作（OpenDAL 内建，零容器依赖）。
- smoke：fs（临时目录）/ MinIO（s3）/ openssh 容器（sftp，M3）；
  framed 客户端直接复用 `sidecar_client.py`。
- 对标清单：tiny-rdm Fs 22 方法 × 状态（对齐/降级/不适用+原因）+
  OpenDAL 服务覆盖矩阵。

---

## 5. 共性工程

### 5.1 sidecar 运行时（**仅 LDAP 走此 Go 路线**；Files 走 Rust 栈）

- **DBX 官方提供 Go SDK**：模块 `github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk`
  （随 `@dbx-app/plugin-cli` sdk-root 捆绑，宿主 worktree `plugins/sdk/go/` 同源）；
  CLI 官方脚手架 `dbx-plugin create --template go`。
- 传输为 **stdio-jsonl**（NDJSON；manifest `entrypoints.backend.transport`
  枚举 `stdio-jsonl | stdio-framed`），SDK 内建 `plugin/initialize` 应答与
  事件推送；每请求一 goroutine（handler 需并发安全）；单条 JSON ≤8MiB；
  **无二进制帧** → LDAP 无二进制需求，不受影响。
- Files 插件（v3）改用 Rust 的 dbx-plugin-sdk（stdio-framed + 二进制通道，
  与 ssh-sftp 插件同栈）——大文件传输走二进制帧而非 base64。
- 原计划「新写 Go 版帧协议库（300-500 行）」取消；M0 缩小为脚手架/打包/
  宿主端到端联调验证（任务清单见 `IMPL_PLAN_M0_COMMON.zh-CN.md` §6），
  实施细节以该文档为准。

### 5.2 目录结构模板（两插件同构）

```
dbx-<name>-plugin/
├── manifest.json
├── dbx-plugin.toml
├── assets/plugin.svg
├── frontend/            # Vue3 + vite（照 ssh-sftp 插件工程配置）
│   └── src/lib/i18n.ts  # 七语
├── backend/
│   ├── （LDAP）Go module：cmd/plugin + internal/{lifecycle,store,ldapconn,ldapgssapi}
│   └── （Files）Rust crate：src/{main,model,engine,transfers,policy,store}.rs
├── scripts/             # build.sh / test.sh / smoke_*.py / sidecar 客户端
└── docs/FEATURE_PARITY.zh-CN.md
```

### 5.3 打包与平台矩阵

- 首期与 ssh-sftp 插件一致：darwin-arm64（开发机主用）+ darwin-amd64；
  linux-amd64/arm64、windows 按宿主分发策略排期。
- LDAP（Go）：纯 Go 交叉编译（`CGO_ENABLED=0`）。
- Files（Rust）：opendal 纯 Rust；feature 白名单控制体积（预估 ~10-20MB），
  依赖许可清单归档（Apache-2.0 为主）。
- 平台矩阵两插件一致：darwin-arm64 优先，其余按宿主分发策略排期。

---

## 6. 里程碑

| 阶段 | 内容 | 出口标准 |
|---|---|---|
| M0（spike，两插件共用） | 官方 Go SDK 脚手架验证 + 最小插件装进测试宿主 | 宿主工作台能调通 echo/test；CLI 打包路径验证（详见 IMPL_PLAN_M0_COMMON） |
| M1 LDAP 核心 | simple/anonymous + ldaps/StartTLS + search/entry CRUD/树/编辑器 + 安全策略 | smoke 全绿，对标清单 core 全勾 |
| M2 Files 核心 | Rust sidecar + OpenDAL：fs/s3(minio)/webdav/ftp + via_iter 透传 + 浏览/读写/复制移动 + 二进制帧传输槽 | MinIO 集成用例过 |
| M3 进阶 | LDAP: kerberos/ntlm/ntlm_hash/digest_md5/external + unauthenticated；Files: sftp 后端 + 异步传输/进度/取消 + 审计 + 云盘精选模板 | GSSAPI 容器用例过；传输可取消、进度可见 |
| M4 收尾 | MCP 工具（ldap 14 个 + fs 侧）、filesystem-provider 评估、七语查漏 | 与 ssh-sftp 插件 mcp.rs 同构验收；对标矩阵全勾 |

（每阶段遵循仓库既有「单测 + smoke + 对标清单 + 七语」完成定义。）

---

## 7. 风险清单

| 风险 | 影响 | 缓解 |
|---|---|---|
| ~~宿主/CLI 对非 Rust sidecar 支持未知~~ | 已解除 | 官方 Go SDK + `create --template go` 存在（IMPL_PLAN_M0_COMMON §0）；残余为 jsonl 宿主端到端联调确认 |
| OpenDAL 能力差异（rename/copy/presign 按后端；无容量查询/chmod） | 功能面 | capabilities 运行时透出 + job 降级；缺口表显式声明（Files 文档 §5.5） |
| Kerberos 在端侧环境（krb5.conf/keytab 路径）可用性 | M3 | tiny-rdm 已有临时 krb5.conf 写入方案可移植；容器集成测试兜底 |
| `database_type` 枚举是否允许 `ldap`/`storage` | manifest | 已确认自由标识符无枚举限制（manifest.schema.json:191）；仅剩宿主表单展示验证 |
| 大文件读写走 base64 方法内存峰值 | 稳定性 | Files 改 Rust 二进制帧后主路径消除（仅 web 降级路径保留 base64）；LDAP 不涉大文件 |
| OAuth 类云盘（gdrive/onedrive/dropbox）需要浏览器回调 | M3+ | opendal-custom 透传手填 token 先覆盖；精选模板 M3 补 |

---

## 8. 决策记录（2026-08-28 确认）

用户确认项：
- **D0** 两个独立插件，不集成进 `io.dbx.ssh-sftp`。✅
- **D1** sidecar 语言：**两插件统一 Go**（Go 生态更好，可整体复用 tiny-rdm
  领域代码）。✅ *（Files 部分已被下方 v3 修订取代）*
- **D2** rclone 驱动：**内嵌库**（librclone，沿袭 tiny-rdm），沿用 Go。✅
  *（已被下方 v3 修订取代）*
- **D4** 特性范围：**全量对齐 tiny-rdm**，分期交付不裁剪（含 8 种认证、
  rclone 全后端透传、MCP 工具）。✅ *（Files 侧对齐目标已按 v3 重定义为
  OpenDAL 服务矩阵 + 显式缺口表；LDAP 侧不变）*

设计定项（遵循 DBX 插件既有约定）：
- **D3** 连接配置归宿主 connection-provider，插件不持久化凭据。✅
- **D5** 不重复宿主：传输层走 DBX，filesystem-provider 首期不做（Phase 2 评估）。✅
- MCP 工具排期 M4（对齐 tiny-rdm 的 MCP 能力面，纳入范围）。✅

**v3 修订（2026-08-28，用户决策）**：
- **弃 rclone + Go**：Files 插件改 **Apache OpenDAL（Rust crate，0.58.x）
  + Rust sidecar**（dbx-plugin-sdk，stdio-framed + 二进制帧）；LDAP 维持
  Go 不变。
- 特性对齐目标重定义：以 OpenDAL 服务矩阵为准（63 services feature，
  编译期白名单），能力缺口（容量查询/Chmod/部分后端 presign/服务端
  sync）显式声明，不再追求 rclone 全后端逐一对齐。
- 实施细节见 `IMPL_PLAN_DBX_FILES.zh-CN.md`（v3）。

## 9. 后续候选（第三批盘点，2026-08-28 聚焦版定论）

tiny-rdm 其余能力的完整评估见 `CAPABILITY_CANDIDATES.zh-CN.md`（聚焦版），
**结论：迁移范围收敛为三插件**——io.dbx.ssh-sftp（已存在，按其自身对标清单
继续增强）、io.dbx.ldap、io.dbx.files；**不做任何新插件**：

- ❌ `io.dbx.mcp-hub`（MCP 控制台）——放弃；
- ❌ playbook / trigger（编排与调度）——多余且受沙箱插件间隔离约束；
- ❌ 审批模式包 / LLM 顾问——砍掉；三插件保留既有的
  「危险操作确认 + audit.jsonl」语义（各 M4 落地），不再扩展。
- ssh-sftp 的 batch-send（多终端批量广播）挂起：依赖宿主多会话语义
  （ssh-sftp 仓 docs/FEATURE_PARITY_BATCH3.zh-CN.md:94 已评估为架构级）。
- kafka/结构同步/Redis 三件套/传输层等不迁移（含可选宿主反馈建议，
  见该文档 §2）；判定依据为沙箱三硬约束（§3）。
- **后续 agent 实施入口**：`~/btroot/dbx-plugins/README.md`（工作区索引）
  → `shared/IMPL_PLAN_M0_COMMON.zh-CN.md` §6（M0 任务）→
  `ldap/docs/IMPL_PLAN_DBX_LDAP` M1 ∥ `files/docs/IMPL_PLAN_DBX_FILES` M2
  （可并行）→ 两文档各自的 M3/M4。
