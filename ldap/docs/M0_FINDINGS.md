# M0 发现记录（L-A 路：脚手架 / 生命周期 / 连接层）

> 记录人：L-A（dbx-ldap-plugin 脚手架、lifecycle、store、ldapconn 连接层）。
> 日期：2026-08-28。工具链：go 1.27.0、dbx-plugin CLI 0.1.0、node 22.21.0。

## 1. M0-T1 核对结论

| 项 | 结论 |
|---|---|
| `dbx-plugin.toml` language 默认 | `create --template go` 生成 `[backend] language = "go"` ✓ |
| manifest transport 默认 | 脚手架 manifest **不写 transport**；宿主 `crates/dbx-core/src/plugins/manifest.rs:89-103` serde default = `StdioJsonLines`（stdio-jsonl），与 Go SDK 传输一致。本仓已**显式**写 `"transport": "stdio-jsonl"`（对齐 M0 文档 §2.1 骨架） |
| `database_type: "ldap"` | 自由标识符，schema `$defs/identifier` 直接可用 ✓ |
| 脚手架 `engines` | 模板生成 `dbx >=0.5.68 / host_api "1"`；本仓按 M0 骨架改为 `>=0.5.77 / >=1.0.0`（与 ssh-sftp 一致） |

## 2. 框架问题（建议进 FRAMEWORK_FEEDBACK）

### 2.1 CLI 的 go.work 硬编码 `go 1.22`（阻塞级，已用本机 shim 绕过）

- `plugins/sdk/cli/src/lib.rs::build_go_backend`（:1050-1074）：sdk-root 存在时
  CLI 生成临时 go.work（**硬编码 `go 1.22`**）并设 `GOWORK` 再跑 `go build`。
- go-ldap **v3.4.13 上游 go.mod 即 `go 1.24.0`**，go 要求 workspace 的 go 指令
  ≥ 主模块 go 指令 → `dbx-plugin package` 报
  `module . listed in go.work file requires go >= 1.24.0, but go.work lists go 1.22`。
- 主模块降回 go 1.22 也不行（`go build` 要求先 tidy 升级）。
- **建议修复**：CLI 生成 go.work 时读取主模块 go.mod 的 go 指令（或干脆
  `go 1.24`/不写版本）。在此之前，本机打包用 go shim 剥掉 GOWORK：

  ```bash
  # /tmp/go-shim/go（PATH 前插），打包：PATH="/tmp/go-shim:$PATH" dbx-plugin package .
  #!/bin/bash
  [ -n "$GOWORK" ] && unset GOWORK
  exec /opt/homebrew/bin/go "$@"
  ```

  剥掉 GOWORK 后 go build 走 `backend/go.mod` 里的 replace 解析 SDK
  （指向 `~/btroot/dbx-plugin-host-worktree/plugins/sdk/go/dbx-plugin-sdk`，
  与 npm 包 sdk-root 内容 diff 为零），产物等价。

### 2.2 Go 插件 main 必须平铺在 `backend/` 根

- CLI 构建固定 `cd backend && go build . `（lib.rs:1054），不支持
  `backend/cmd/plugin/` 布局；实施文档 §1 的 `cmd/plugin/main.go` 与之冲突。
- 已按框架约束落地为 `backend/main.go`（包名 main）。

### 2.3 SDK 不在公网 module proxy

- `go mod tidy` 报 `unknown revision plugins/sdk/go/dbx-plugin-sdk/v0.1.0`。
- 本地构建必须在 go.mod 写 replace 指向本地 SDK（见 §2.1）；`dbx-plugin
  package` 打包时 CLI 自己注入（GOWORK use），两路径已各自验证。

### 2.4 manifest 条件字段不支持 AND 组合

- schema `$defs/fieldCondition` 仅 `{field, one_of[]}` 单条件。
- §4 的 `krb_password`（kerberos ∧ credential_type=password）无法精确表达，
  现按 `visible_when: krb_credential_type ∈ [password]` 近似
  （credential_type 字段本身已由 auth_type 联动显隐，实际可达性等价，
  仅表单联动层级多一层）。

### 2.5 `url` 绑定 host 的形态（M0-T6 待确认，沿袭）

- 按主方案把整个 `ldap(s)://host:port` URL 放 `connection.host`
  （binding host）。sidecar 已按此解析：拨号一律 `runtime.host:port`，
  URL 仅取 scheme 与逻辑主机（TLS SNI）。若 M0-T6 结论为拆 host/port，
  只需改 `NewProfileFromLifecycle`（service.go）一处。

## 3. 连接层实现决策（给 reviewer）

- **凭据与 Profile 分离**：L-B 的 types.go `Profile` 无 BindPassword 字段；
  bind_password/ntlm_hash 从 lifecycle secret 读出后只存
  `connEntry.bindPassword/ntlmHash`（内存），不进 Profile、不落盘、不进
  日志/审计/事件。
- **withConn 重连语义**：tiny-rdm `withConn(:970)` + `ldapNeedsReconnect(:1963)`
  原样移植（dial.go），失败判定可重连时关闭旧连 → 重连一次 → 重试操作。
- **connection/test**：dial+bind+（有 base_dn 时）base scope 读 1 条
  （attributes "1.1" 只探存在性），不缓存连接。
- **审计折算**：内部 result `success|blocked|error` → audit.jsonl
  `ok|denied|error`（M0 §4 格式）；事件 `ldap/audit` 与落盘共用同一份
  非敏感数据（`AuditRecord`）。
- **领域方法实现进度**（2026-08-28 复核更新）：L-A 原始交付为 panic 桩；
  L-B 路现已在 operations.go 完成全部 12 个领域方法实现，
  `go test ./...` 全仓绿（含 ldapconn 包）。签名契约见 M0_FINDINGS 交接清单。

## 4. 打包/验证记录

| 验证 | 结果 |
|---|---|
| `go build ./... && go vet ./...` | PASS |
| `go test ./internal/lifecycle ./internal/store` | ok（9+9 用例） |
| `gofmt -l`（本路文件） | 干净（ldapconn/policy_test.go 属 L-B，仍有格式问题） |
| sidecar JSONL 冒烟（/tmp/ldap_sidecar_smoke.py） | 7 项 PASS：initialize / test 业务错误(-32000) / connect 惰性 / disconnect 幂等 / -32601 / connect 缺 id / stdin EOF 退出码 0 |
| `dbx-plugin package .`（go shim） | `dist/io.dbx.ldap-0.1.0-darwin-arm64.dbxp`（manifest+bin+ui+assets+checksums） |
| `go test ./...` 全仓 | lifecycle/store ok；**ldapconn 包 FAIL（L-B 的 policy/schema 实现与测试不匹配，非 L-A 范围）** |
