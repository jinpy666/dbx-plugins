# dbx-ldap-plugin（io.dbx.ldap）

DBX 的 LDAP 控制台插件：DN 树浏览、RFC 4515 搜索（分页）、条目
查看/编辑/新增/删除/改名、RootDSE 与 Schema 元数据、LDIF/CSV 导出、
搜索预设；8 种认证（simple/anonymous/unauthenticated/kerberos(GSSAPI)/
ntlm/ntlm_hash/digest_md5/external）；DN 白名单 + 屏蔽属性 + 只读 +
审计安全策略。能力移植自 tiny-rdm `backend/services/ldap_service.go` 等。

## 状态

**待实施**（方案定稿，见 `shared/`）。里程碑：M0（公共基线）→ M1（核心）→
M3（进阶认证）→ M4（MCP 工具 + 收尾）。

## 技术形态

- sidecar：**Go**（官方 Go SDK，`stdio-jsonl`），二进制 `dbx-plugin-ldap`
- 依赖：`go-ldap/ldap/v3 v3.4.13`、`jcmturner/gokrb5/v8 v8.4.4`（纯 Go，CGO=0）
- 连接：宿主 connection-provider（`database_type: "ldap"`），凭据走
  secret binding，插件不持久化
- 拨号：`runtime.host:port`（DBX 传输层出口），TLS SNI/SPN 用逻辑主机名

## 文档

- 实施文档（唯一工作来源）：[docs/IMPL_PLAN_DBX_LDAP.zh-CN.md](docs/IMPL_PLAN_DBX_LDAP.zh-CN.md)
  ——迁移映射、manifest 字段全表、sidecar 方法契约、安全策略、前端组件、
  smoke 场景 S1–S10、任务表 L1/L3/L4
- 公共基线：`../shared/IMPL_PLAN_M0_COMMON.zh-CN.md`（lifecycle/审计/测试基建）
- 总提案与决策：`../shared/PLUGIN_PROPOSAL_LDAP_RCLONE.zh-CN.md`

## 脚手架入口

M0-T1 通过后，`dbx-plugin create --template go` 生成骨架并入本目录，
scripts/ 四件套（build/test/smoke/sidecar_client_jsonl）按
`shared/IMPL_PLAN_M0_COMMON.zh-CN.md` §5 模板落地。
