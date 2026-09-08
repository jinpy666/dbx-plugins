# M0 共用基础实施文档：Go sidecar 基线（dbx-ldap-plugin / dbx-files-plugin 共用）

> 本文是两插件实施文档（`IMPL_PLAN_DBX_LDAP.zh-CN.md` / `IMPL_PLAN_DBX_FILES.zh-CN.md`）
> 的公共基线：SDK 契约、manifest/打包、连接生命周期、本地数据与审计、测试基建。
> 完成本文任务清单后，两插件在其上并行开发。
>
> **v3 修订后的适用范围**：Files 插件已改 **Rust + OpenDAL** 路线（弃 rclone+Go）。
> 本文的 **Go SDK / stdio-jsonl / jsonl 客户端部分仅适用于 LDAP 插件**；
> Files 插件复用 ssh-sftp 插件已验证的 **Rust 栈**（dbx-plugin-sdk，
> stdio-framed + 二进制帧，见 §附录 A）与现有 `sidecar_client.py`（framed），
> 其 Rust 侧验证并入 Files 文档 F2-1，不走本清单的 Go 任务。
> §3（lifecycle 契约）、§4（数据/审计）、§2.2-2.3（打包/安装）、§5.2-5.3
> （smoke 语义/容器）对两插件通用。

## 0. 相对原方案的重大修订：无需自写帧协议库

原方案 §5.1 计划「新写 Go 版 stdio-framed 协议库（300-500 行）」。调研确认
**DBX 官方已提供 Go SDK**，M0 从「自写协议库」降级为「用官方 SDK + 脚手架验证」：

| 事实 | 出处 |
|---|---|
| Go SDK 存在，模块 `github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk`（go 1.22，单文件 244 行） | CLI sdk-root：`@dbx-app/plugin-cli/sdk-root/plugins/sdk/go/dbx-plugin-sdk/sdk.go`；宿主 worktree `plugins/sdk/go/` 同源 |
| 传输为 **stdio-jsonl**（NDJSON：`bufio.Scanner` 逐行读、写以 `\n` 结尾） | sdk.go `Serve()` / `Emitter.write()` |
| manifest 传输枚举：`stdio-jsonl` / `stdio-framed` | `plugins/manifest.schema.json:78` |
| CLI 官方脚手架：`dbx-plugin create --template go` | `plugins/GETTING_STARTED.zh-CN.md:72-80, 205-212` |
| `database_type` 是自由标识符（`$defs/identifier`），`ldap` / `storage` 可直接用 | manifest.schema.json:191 |

原方案风险表第一条「宿主/CLI 对非 Rust sidecar 支持未知」**解除**；残余验证点
缩小为 §6 任务清单（jsonl 传输在真实宿主的端到端联调等，均为低风险确认项）。

## 1. Go SDK 契约（两插件 sidecar 的公共运行时）

### 1.1 API 面（`dbxpluginsdk` 包）

```go
metadata := dbxpluginsdk.Metadata{
    ID:           "io.dbx.ldap",          // 插件 id
    Version:      "0.1.0",                // 与 manifest.version 一致
    Capabilities: []string{"connections", "events"},
}
server := dbxpluginsdk.NewServer(metadata, handler)
// 可选 server.WithIO(in, out, err) 用于测试
if err := server.Serve(); err != nil { ... }
```

- `Handler` 接口：`Handle(ctx RequestContext, method string, params json.RawMessage,
  emitter *Emitter) (any, *PluginError)`；`HandlerFunc` 适配裸函数。
  方法分发（`<域>/<动作>` 字符串匹配）由插件自己写 `switch`，与 ssh-sftp 的
  `main.rs::handle_request` 同构。
- `plugin/initialize` 由 SDK 内建应答：校验 `host.protocolVersions` 含 1，
  返回 `{protocolVersion, capabilities, plugin:{id, version}}`（sdk.go:181）。
- `Emitter.Event(method, params)` 向宿主发通知（事件）；stderr 为诊断日志通道，
  **stdout 只允许协议消息**。
- `PluginError{code, message, data}`；方法未找到用 `MethodNotFound(method)`
  （-32601）。业务错误统一 `-32000`（对齐 ssh-sftp 的 `to_plugin_error`）。

### 1.2 并发模型与约束（写代码前必须知道）

| 约束 | 依据 | 对策 |
|---|---|---|
| **每个请求一个 goroutine**，Handler 必须并发安全 | sdk.go `Serve()` 中 `go func(request)` | 连接池 / job 表全部加锁；按 connectionId 串行化的操作用 per-connection 队列（Files 插件 §7） |
| **无二进制帧**（jsonl 传输只有 JSON 消息） | sdk.go 全文无 binary 解析 | 大文件传输用 **JSON 分块 + base64**（Files 插件方法契约）；LDAP 全程 JSON 无影响 |
| 单条 JSON ≤ 8 MiB（`maxJSONBytes`） | sdk.go:16 | base64 分块原始字节 ≤ 1 MiB（编码后 ~1.37 MiB，安全）；`files/read` 预览 ≤ 2 MiB |
| 事件与响应共用 stdout 写锁 | `Emitter.mutex` | 直接用 SDK 的 Emitter，不自行写 stdout |

### 1.3 SDK 引用方式

- 常规：CLI 打包时自动携带（`@dbx-app/plugin-cli` npm 包内置 sdk-root），
  `dbx-plugin create --template go` 生成的 go.mod 已指向该模块。
- 联调 SDK 改动时：`export DBX_PLUGIN_SDK_ROOT=<dbx worktree>` 后打包
  （GETTING_STARTED.zh-CN.md:216-219）。
- 固定 SDK 版本：go.mod 记录 CLI 当前版本（`dbx-plugin --version` 输出写进
  各插件 README，保证「打包的 SDK = 测的 SDK」）。

## 2. manifest 与打包基线

### 2.1 公共 manifest 骨架（差异字段见各插件文档 §manifest）

```jsonc
{
  "manifest_version": 1,
  "id": "io.dbx.ldap",                    // 或 io.dbx.files
  "engines": { "dbx": ">=0.5.77", "host_api": ">=1.0.0" },
  "permissions": ["host.events", "host.workbench"],   // files 插件追加 host.filesystem
  "entrypoints": {
    "backend": {
      "transport": "stdio-jsonl",          // Go SDK 传输，与 ssh-sftp 的 framed 不同
      "executable": "bin/dbx-plugin-ldap"  // 打包后 bin/<platform>/
    },
    "ui": { "root": "ui", "entry": "ui/index.html" }
  },
  "contributions": [ /* connection-provider + workbench，见各插件文档 */ ]
}
```

- `visible_when` / `required_when` 条件字段：宿主 worktree 已支持，按
  ssh-sftp manifest 的写法照抄。
- 七语 `localizations`：zh-CN / zh-TW / en / es / it / ja / pt-BR 全量
  （漏一语不报错，靠 review + 脚本检查，见 §5.4）。

### 2.2 打包与平台

- `dbx-plugin.toml`：`[backend] language = "go"`（以 `create --template go`
  产物为准，M0-T2 验证）；`[package] include = ["assets", "ui"]`。
- 构建命令链（scripts/build.sh 模板）：
  `go test ./... && go build -trimpath -ldflags "-s -w" -o bin/… ./backend`
  → `dbx-plugin package .`。
- 平台矩阵首期：darwin-arm64（开发主用）+ darwin-x64；linux-x64/arm64、
  windows-x64 按发布节奏补。原生插件必须分平台构建，本机只出当前平台包。
- 交叉编译注意：LDAP（Go）保持纯 Go（`CGO_ENABLED=0`，gokrb5 纯 Go、
  不引 go-sqlite3——配置存储已归宿主，方案 D3）；Files（Rust）opendal
  纯 Rust，feature 白名单控体积。

### 2.3 安装验证（对齐 ssh-sftp 的 install.sh 语义）

- 测试宿主：`~/btroot/dbx-plugin-host-worktree/target/debug/bundle/macos/DBX.app`
  （框架分支构建；正式版 DBX.app 无插件框架装了没用；相对各插件仓为
  `../../dbx-plugin-host-worktree/…`）。
- 安装走官方 PluginPackageInstaller；宿主二进制有缓存坑——宿主侧改动后删
  `target/release/examples/install_plugin` 重编。
- 双冒烟验收：对**安装副本**二进制跑 smoke（`DBX_PLUGIN_SIDECAR=<安装路径>
  /bin/darwin-arm64/<binary> python3 scripts/smoke_test.py`）。

## 3. 连接生命周期契约（两插件统一实现）

### 3.1 lifecycle params 形状（宿主 → sidecar）

来源：ssh-sftp `backend/src/model.rs:194-234`（`from_lifecycle_params`）+
`scripts/sidecar_client.py:205`。Go 侧写同构解析器（`internal/lifecycle`）：

```jsonc
{
  "provider": { "id": "io.dbx.ldap.connection", "databaseType": "ldap" },
  "connection": {
    "id": "uuid",                  // 连接 id，所有状态的键
    "name": "prod-ldap",
    "host": "ldap.example.com",    // binding: host / port / username / name / password
    "port": 636,
    "username": "",
    "password": "",
    "external_config": {           // binding: config 的字段，key=manifest field key
      "auth_type": "simple", "base_dn": "dc=example,dc=com", ...
    },
    "connection_secrets": {        // binding: secret 的字段
      "bind_password": "..."
    }
  },
  "runtime": { "host": "127.0.0.1", "port": 6360 },  // DBX 传输层拨号端点
  "operationId": "..."             // Host 1.1 才有；缺失时本地 uuid 兜底
}
```

**binding 落点映射**（manifest field `binding` → connection JSON 位置）：

| binding | 落点 | 说明 |
|---|---|---|
| `name` / `host` / `port` / `username` | `connection.*` 顶层 | 标准字段 |
| `password` | `connection.password` | 明文密码类 |
| `config` | `connection.external_config.<field_key>` | 非敏感配置（snake_case key） |
| `secret` | `connection.connection_secrets.<field_key>` | 敏感凭据，宿主管加密存储 |

**拨号语义**（方案 D5，不重复宿主）：网络层一律连 `runtime.host:port`
（DBX 隧道/代理出口）；逻辑主机（TLS SNI、Kerberos SPN、rclone host 参数展示）
用 `connection.host`。两插件所有出网拨号遵守此规则。

### 3.2 三个生命周期方法的行为约定

| 方法 | 行为 |
|---|---|
| `connection/test` | 立即建真连接并做最小验证（LDAP：dial+bind+读 base DN；Files：`operations/list` 根目录），成功返回 `{success:true, message}`，失败返回业务错误；不残留状态 |
| `connection/connect` | 接收并缓存连接配置（**不**立即建真连接或惰性建连由插件定义，见各插件 §连接设计），幂等（重复 connect 覆盖旧配置并按需断开旧实例） |
| `connection/disconnect` | 按 `connection.id` 释放该连接下全部资源（LDAP 连接池条目 / Files remote 注册与 job），幂等 |

- 工作台**不接收密码字段**：连接后工作台方法只带 `connectionId`；
  sidecar 内凭据只留内存。
- 进程生命周期 = 插件生命周期（sidecar 是插件级共享进程）：
  stdin EOF 即退出前清理（`Serve()` 返回后 defer 清理全部连接）。

## 4. 本地数据与审计基线（`DBX_PLUGIN_DATA_DIR`）

```
DBX_PLUGIN_DATA_DIR/                 # 缺省 fallback: $TMPDIR/dbx-plugin-data/<plugin-id>
├── prefs.json                       # UI 偏好（列宽/面板开关等，非敏感）
├── presets.json                     # LDAP 搜索预设（明文，不含凭据）
├── transfers.json                   # Files 传输历史（上限 200 条，环形覆盖）
├── audit.jsonl                      # 写操作审计（append-only）
└── cache/schema-<connHash>.json     # LDAP schema 缓存（可删，重建无害）
```

- 环境变量 `DBX_PLUGIN_DATA_DIR`（ssh-sftp `main.rs:744` 同源语义）；
  启动时 `os.MkdirAll`。
- **凭据红线**：以上任何文件不落密码/私钥/token；日志（stderr）与事件不携带
  凭据字段；审计记录 target 只记路径/DN，不记值。
- 审计记录格式（与 ssh-sftp 对齐）：JSONL，每行
  `{"time":"RFC3339","connectionId":"…","action":"ldap/entry/delete","target":"dn=…","result":"ok|denied|error"}`。

## 5. 测试基建

### 5.1 NDJSON 版 sidecar 客户端（`scripts/sidecar_client_jsonl.py`，**仅 LDAP 需要**）

现有 `sidecar_client.py` 说 stdio-framed（5 字节帧头）。jsonl 版更简单，
接口保持一致（`start_default / initialize / request / wait_event / close`）：

- 发送：`json.dumps({"jsonrpc":"2.0","id":n,"method":m,"params":p}) + "\n"`；
- 接收：按行读 stdout，`id` 匹配取 `result`/`error`，无 `id` 的入 `events`；
- `lifecycle_params(connection)` 辅助函数照抄 framed 版。
- 两插件各自 vendor 一份（仓库独立），或 M0 期间先放 ssh-sftp 仓 scripts/
  供复制。

### 5.2 smoke 脚本模板（`scripts/smoke_test.py`）

对齐 ssh-sftp 语义：**未注册方法 SKIP 而非 FAIL**；对容器跑不通的环境变量
未设时整段 SKIP。公共骨架：

```
initialize → connection/test → connection/connect → <领域方法序列> →
connection/disconnect → stdin 关闭进程退出
```

### 5.3 集成容器基线

| 容器 | 服务 | 用于 |
|---|---|---|
| `linuxserver/openssh-server`（ssh-sftp 已有配方） | SSH 2222 | Files 插件 sftp 后端（M3） |
| `bitnami/openldap`（或 `docker-compose.ldap-test.yml` 同款 + ldap-seed） | LDAP 389/636 | LDAP 插件全量（M1） |
| `minio/minio` 单容器 | S3 9000/9001 | Files 插件 s3/minio（M2） |

### 5.4 go test 与静态检查

- 单测只测纯逻辑（解析/校验/映射/序列化），不连网；需要协议层的用
  `server.WithIO(io.Pipe())` 做 in-process 黑盒（SDK 自带该钩子，sdk_test.go 同款）。
- `go vet`、`gofmt -l` 进 build.sh；七语完整性用脚本比对 i18n key 集合
  （对齐 ssh-sftp「漏一语靠 review」的痛点，这里直接脚本化）。

## 6. M0 任务清单与验收标准

### 6.0 实施顺序总览（给实施 agent 的 roadmap）

```
阶段 0（本节 T1-T6，Go 路线部分服务 LDAP）
  └─ M0 验收（T1-T6 全过）
       ├─ 阶段 1a：LDAP M1（IMPL_PLAN_DBX_LDAP §9 L1-1…L1-8）
       └─ 阶段 1b：Files F2-0/F2-1（IMPL_PLAN_DBX_FILES §11，Rust 栈
            自验：manifest + framed sidecar 装进测试宿主——与 ssh-sftp
            同栈，风险低，无需等 Go 任务）
            （1a/1b 可并行）
       ├─ 阶段 2：LDAP M1 收尾 ∥ Files M2 主体（F2-2…F2-9）
       └─ 阶段 3+：各自 M3 → M4（按各实施文档任务表顺序执行）
```

- 每个任务以各实施文档的 DoD 列为准；仓库级完成定义 =
  「单测 + smoke + 对标清单更新 + 七语」四件套（本仓 skill 硬性约定）。
- 遇宿主侧不确定项（表单形态/隧道 API/多会话语义），一律：
  记录进对应插件 PROGRESS 文档 + 按 optional 降级/备份方案继续，不阻塞主线。

### 6.1 任务清单（适用范围见行末标注）

| # | 任务 | 产出 | 验证方式 | 适用 |
|---|---|---|---|---|
| M0-T1 | `dbx-plugin create --template go` 生成最小插件，核对 toml `language` 与 manifest transport 默认值 | 临时脚手架（验证后转正为 LDAP 仓初始提交） | 产物 diff 记录进本文档附录 | LDAP |
| M0-T2 | 最小 Go sidecar：`connection/test`（固定 success）+ `echo` + 一个事件 | 20 行 handler | `sidecar_client_jsonl.py` 驱动全通 | LDAP |
| M0-T3 | `dbx-plugin package` 打包 → 测试宿主安装 → 工作台调通 test/echo/事件 | `.dbxp` + 安装日志 | 宿主插件中心可见、工作台无「正在连接」挂死 | LDAP（Files 的同类验证在 F2-1，Rust 栈） |
| M0-T4 | 脚本四件套模板：build.sh / test.sh / smoke_test.py / sidecar 客户端 | scripts/（LDAP 用 jsonl 客户端；Files 直接复制本仓 framed 版 `sidecar_client.py`） | test.sh 全绿（含 SKIP 语义） | 两插件 |
| M0-T5 | lifecycle 解析器 + prefs/audit 读写（`internal/lifecycle`、`internal/store`，Go） | 两包 + 单测 | go test 全绿 | LDAP（Files 按 ssh-sftp 的 model.rs/store 模式同构实现，见其 F2-1/F2-2） |
| M0-T6 | 宿主侧确认三件：① jsonl 传输端到端（事件/initialize）② 连接表单 `url` 绑定 host 形态与 visible_when 联动 ③ **宿主隧道 API 形态**（Files via-dbx-ssh 前置） | 联调记录（含截图/结论写回本文档与各实施文档对应小节） | 工作台收到事件；表单显隐正确；隧道 API 结论明确 | ①② LDAP；③ Files |

**验收 = T1-T6 全过**。此后脚手架复制为两插件仓初始状态，进入
LDAP M1 / Files M2（两插件实施文档）。

## 附录 A：jsonl 与 framed 的差异备忘（给熟悉 ssh-sftp 的 reviewer）

| 维度 | ssh-sftp（Rust, stdio-framed） | 本方案（Go, stdio-jsonl） |
|---|---|---|
| 帧格式 | 5 字节头 `[kind:u8][len:u32 BE]` | 每行一条 JSON（`\n` 结尾） |
| 二进制通道 | 有（`ssh/terminal/*`、`sftp/upload/*`，256 KiB 块 + 8 字节 BE 前缀） | 无 → 大文件走 JSON+base64 分块方法 |
| 消息上限 | 帧长 u32 | 8 MiB / 条（SDK maxJSONBytes） |
| initialize | SDK 内建 | SDK 内建（同返回结构） |
| 并发 | tokio runtime | 每请求一 goroutine |
