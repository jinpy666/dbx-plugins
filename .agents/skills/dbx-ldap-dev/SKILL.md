---
name: dbx-ldap-dev
description: DBX LDAP 插件（io.dbx.ldap，Go sidecar）的开发入口：实施文档、协议形态、脚本与测试容器。当用户在 ldap/ 目录下开发、调试 LDAP 认证/浏览/编辑能力、跑 LDAP smoke 或排查 Go sidecar / stdio-jsonl 协议问题时使用。
---

# DBX LDAP 插件（io.dbx.ldap）开发入口

Go sidecar（官方 Go SDK，`stdio-jsonl`），二进制 `dbx-plugin-ldap`；依赖
`go-ldap/ldap/v3` + `gokrb5/v8`（纯 Go，CGO=0）。能力：DN 树浏览、RFC 4515
搜索（分页）、条目查看/编辑/新增/删除/改名、RootDSE/Schema、LDIF/CSV 导出、
8 种认证、DN 白名单 + 屏蔽属性 + 只读 + 审计安全策略。

- **唯一工作来源**：`ldap/docs/IMPL_PLAN_DBX_LDAP.zh-CN.md`（迁移映射、manifest
  全表、方法契约、smoke 场景 S1–S10、任务表 L1/L3/L4）；进度记
  `ldap/docs/PROGRESS-*.zh-CN.md`。
- 通用规范（环境/硬性约定/故障速查）：skill `dbx-plugin-dev`；宿主验证：skill `dbx-host-e2e`。
- 连接凭据走宿主 connection-provider + secret binding；拨号走 `runtime.host:port`，
  TLS SNI/SPN 用逻辑主机名。

## 脚本与测试

```bash
scripts/build.sh             # 前端三件套 + go build + dbx-plugin package
scripts/test.sh              # 全套：typecheck/test/build → go vet/test → 打包 → smoke
scripts/smoke_test.py        # 主冒烟（无 OpenLDAP 容器时整组 SKIP）
scripts/smoke_auth_test.py   # 8 种认证矩阵
scripts/smoke_ldapi_test.py  # ldapi (Unix socket) 拨号
scripts/smoke_container.py   # 测试容器编排入口
scripts/sidecar_client_jsonl.py  # stdio-jsonl 协议客户端（直接驱动 sidecar 调试）
```

- 测试容器：`docker compose -f docker-compose.ldap-test.yml up -d`
  （TLS 用 `docker-compose.ldap-tls-test.yml`）；种子数据见 `scripts/ldap-seed`。
- 后端并行开发期：go test 失败记入 `docs/PROGRESS-C.zh-CN.md` 而非阻塞前端路径；
  未实现方法在 smoke 中必须 SKIP 而非 FAIL。

## 协议要点

- stdio-jsonl：每行一个 JSON 消息（与 ssh/files 的 5 字节帧头不同），
  调试一律用 `sidecar_client_jsonl.py`，勿手搓。
- Kerberos/GSSAPI 纯 Go 实现；NTLM/digest-md5 走对应 LDAP SASL 机制；
  `ldapi://` Unix socket 拨号在 `internal/ldapconn/dial.go`。
