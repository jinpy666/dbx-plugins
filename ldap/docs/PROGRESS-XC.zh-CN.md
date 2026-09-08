# X-C 路进度记录（TLS 容器路径验证 + M3 进阶认证实施）

> 记录人：X-C（dbx-ldap-plugin TLS 路径与 M3 认证）。
> 日期：2026-08-28。工具链：go 1.27.0（darwin/arm64）/ Docker 29.4.0 / dbx-plugin CLI 0.1.0（经 /tmp/go-shim 剥 GOWORK）/ python3.11。
> 凭据红线：容器管理员密码、测试 bind 密码全程仅经环境变量（`LDAP_ADMIN_PASSWORD` → compose 变量替换 → 容器 env / smoke 的 `connection_secrets`），随机生成、未写入任何文件/日志/报告；TLS 服务器证书为一次性 `openssl req -x509` 自签、写入系统临时目录并在收尾删除。SASL/GSSAPI 相关日志与错误不含凭据。
> 前序：M1 已完成（L-A/L-B/L-C/L-D，容器冒烟 S1-S10 10/10，前序记录文档已删除，见 git 历史）。

## 1. 交付总览

| 交付 | 状态 |
|---|---|
| TLS 容器路径（ldaps 636 / StartTLS 389 / tls_verify 语义 / 审计 warning） | **实跑通过**（T1-T5 全 PASS） |
| M3 认证：external / digest_md5 / ntlm / ntlm_hash / kerberos | **实现完成**；容器可测段实跑 PASS，难造段 SKIP+原因或单测覆盖 |
| internal/ldapgssapi（GSSAPI 客户端搬运） | **原样搬运**（`diff -w` 仅 gofmt 空白），go test ok |
| manifest / lifecycle 的 M3 字段接通 | **核对+接线完成**（krb_* 26 字段已在 manifest；lifecycle → Profile 全接通） |
| go build / vet / test（4 包）、S1-S10 回归、打包 | **全绿**（零回归） |

## 2. 任务 1：TLS 容器路径

### 2.1 容器与配置（本路新增）

| 文件 | 说明 |
|---|---|
| `docker-compose.ldap-tls-test.yml` | bitnami/openldap 变体：`LDAP_ENABLE_TLS=yes` + `LDAP_LDAPS_PORT_NUMBER=636`（1389 同时可 StartTLS）；证书文件显式指向 `/certs/openldap.crt/.key/openldap-ca.crt`；文件内零凭据——`LDAP_ADMIN_PASSWORD` 与 `LDAP_TLS_CERTS_DIR`（含一次性自签证书的临时目录）均强制环境变量 |
| `scripts/smoke_auth_test.py` | M3/TLS 专用 smoke（自包含编排，镜像 `smoke_container.py` 配方）：临时目录生成自签证书 → compose up → 就绪探测（1389 admin bind + 636 TLS 握手双条件）→ 跑场景 → 默认 down -v 并删临时证书目录；`--keep` / `--attach` 可调 |

> 备注：bitnami 侧未采用其自动生成证书的路径（不可控、留痕不明），统一由 smoke 脚本生成一次性证书再挂载，收尾即焚。

### 2.2 TLS 场景结果（两轮实跑：本地二进制 + 打包态包内二进制，均 5/5 PASS）

| # | 场景 | 结果 | 证据/语义 |
|---|---|---|---|
| T1 | `ldaps://:636` connection/test，`tls_verify=false` | **PASS** | 自签证书可连：走 `ldapTLSConfig` 的 `InsecureSkipVerify`（dial.go，`//nolint:gosec // 显式用户配置`） |
| T2 | ldaps connect + base search + **审计 warning** | **PASS** | audit.jsonl 出现 `action=ldap/tls-insecure, result=warning`（实施文档 §10 语义：tls_verify=false 连接级显式配置，审计记录一条 warning）；并断言审计记录不泄露密码 |
| T3 | `ldap://:1389` + `use_starttls=true` connection/test | **PASS** | StartTLS 升级路径，同份自签证书 |
| T4 | ldaps + `tls_verify=true` | **PASS** | 自签链被拒（dial 报 TLS/certificate 错）——默认校验不因容器放宽 |
| T5 | ldaps + `use_starttls=true` | **PASS** | dial 层互斥守卫报错（"startTLS cannot be combined with ldaps url"） |

### 2.3 M3/TLS 认证场景（同脚本，实跑 4 PASS + 1 SKIP）

| # | 场景 | 结果 | 说明 |
|---|---|---|---|
| A1 | external（SASL EXTERNAL）over TCP | **PASS** | 真实 `conn.ExternalBind()` 被调用（服务器拒绝 bind——TCP 上无客户端证书，符合预期）；错误文案不再含 "planned for M3"。ldapi/unix socket 成功路径容器难造（bitnami 的 ldapi sock 在容器内、sidecar 在宿主），由单测覆盖真实调用面 + 遗留登记 |
| A2 | digest_md5 | **SKIP** | bitnami/openldap 未编译 cyrus-sasl DIGEST-MD5 机制（`SASL(-4): no mechanism available: Couldn't find mech DIGEST-MD5`）→ SKIP+原因；实现路径已激活（MD5Bind 被调用、SASL host 从 URL 主机解析，单测覆盖） |
| A3 | ntlm | **PASS** | 真实 `conn.NTLMBind` 被调用；OpenLDAP（无 AD）拒绝 NTLMSSP 属预期，断言的不是"成功"而是"真实 bind 路径 + 参数面无回归" |
| A4 | ntlm_hash | **PASS** | (a) 缺 hash → `ntlmHash is required`（参数面）；(b) 带 hash → 服务器 bind 错误（真实 `NTLMBindWithHash` 路径），均非 M1 桩 |
| A5 | kerberos | **PASS** | (a) 缺 realm/kdc/krb5.conf → kerberos 配置错误；(b) 内联 realm+死 KDC（127.0.0.1:18688）→ gokrb5 AS_REQ 网络错误——证明 KDC 实拨与错误面；均非 M1 桩 |

## 3. 任务 2：M3 认证实施（对照表）

### 3.1 参照基线行号 → 本仓函数对照（历史）

| 参照基线源（backend/services/ldap_service.go，已退役） | 本仓位置 | 改造点 |
|---|---|---|
| `bindLDAPConnection` :1210-1264（8 种分发） | `internal/ldapconn/dial.go` `bindLDAPConnection`（新增 `ctx`/`logicalHost`/`bindSecrets` 参数） | target 收敛为 URL 逻辑主机（D5）；`bindSecrets` 聚合 bind/kerberos 两个 secret |
| `bindLDAPKerberosConnection` :1268 | `auth_gssapi.go` `bindLDAPKerberosConnection` | 原样（authzID 从 `profile.AuthzID`） |
| `ldapShouldRetryKerberosWithPort88` :1286 | `auth_gssapi.go` 同名 | 原样（6 条网络错误 hint 表） |
| `ldapKerberosPort88FallbackProfile` :1300 | `auth_gssapi.go` 同名 | 去 KDCNetworkAddress 拨号改写（D5），仅改 KDCPort=88 |
| `resolveLDAPSASLHost` :1470 | `dial.go` `resolveLDAPSASLHost` | target 链收敛为 `profile.SASLHost` → URL 逻辑主机（空时自 URL hostname 兜底） |
| `newLDAPGSSAPIClient` :1493 | `auth_gssapi.go` 同名 | `DisablePAFXFAST(true)` 原样；credential_type 三分支原样；密码经 `bindSecrets.KerberosPassword`（不入 Profile） |
| `prepareLDAPKerberosRuntime` :1519 | `auth_gssapi.go`（收敛进 `newLDAPGSSAPIClient`+`ldapKerberosConfigPath`） | 删 proxy/SSH 路由段（DBX 传输层替代） |
| `ldapKerberosPrincipalValues` :1598 | `auth_gssapi.go` 同名 | 原样（username 含 @REALM 拆分兜底） |
| `ldapKerberosConfigPath` :1625 | `auth_gssapi.go` 同名 | 原样（inline realm+kdc → 临时文件；显式路径 → 直用；`/etc/krb5.conf` 兜底） |
| `ldapWriteTempKrb5Conf` :1649 | `auth_gssapi.go` 同名 | 模板原样；落点 `$DBX_PLUGIN_DATA_DIR/krb5/`（实施文档 §3），不可用退回 os.TempDir；`udp_preference_limit=1` 条件收敛为 `kdcPort != 88`（ForceTCP 仅源于 KDCNetworkAddress 路由，已删）；0600 权限 |
| `ldapKerberosServicePrincipal` :1676 | `auth_gssapi.go` 同名（收敛版） | 本仓无 ExplicitSPN/ServiceName/BypassHostRewrite 字段（manifest §4 未暴露），SPN = `ldap/<URL 逻辑主机小写>` |
| `ldapGSSAPIClientOptions` :1690 | `auth_gssapi.go` 同名 | 原样：integrity 恒开、`qop=auth-conf` → confidentiality、mutual 随开关；qop 缺省 auth |
| `backend/ldapgssapi/client.go`（287 行） | `internal/ldapgssapi/client.go` | **原样搬运**（`diff -w` 与源零差异，仅 gofmt 对齐）；`client_test.go` 同 |
| go-ldap 绑定调用（NTLMBind/NTLMBindWithHash/MD5Bind/ExternalBind/GSSAPIBind） | dial.go 分发直调（go-ldap v3.4.13 API） | — |

### 3.2 数据模型与 lifecycle 接线

- `types.go`：新增 `LDAPKerberosConfig`（CredentialType/Username/Realm/KDCHost/KDCPort/KeytabPath/CCachePath/Krb5ConfPath）+ `NormalizeLDAPKerberosConfig`（credentialType 缺省 password、KDCPort 缺省 88、Realm 大写）；`Profile` 新增 `Kerberos *LDAPKerberosConfig`、`SASLHost`、`SASLQoP`、`SASLMutualAuth`。
- 凭据红线：**kerberos 密码不入 Profile**——`bindSecrets{BindPassword, KerberosPassword}` 只存 `connEntry`（service.go），与 M1 的 bind_password 同语义；`Profile.Redacted()` 继续清 NTLMHash。
- `service.go` `NewProfileFromLifecycle`：接通 `krb_credential_type/krb_realm/krb_kdc_host/krb_kdc_port/krb_keytab_path/krb_ccache_path/krb5_conf_path`（config）与 `krb_password`（secret）；`bind_password`/`krb_password` → `bindSecrets`。SASL 覆盖项 `sasl_host/sasl_qop/sasl_mutual_auth` 也接线（manifest 未暴露，留内部扩展点，缺省对齐参照基线行为）。
- TLS 审计 warning：`service.go` `emitTLSInsecureAudit`——ldaps 或 StartTLS 且 `tls_verify=false` 时 dial 成功后发一条 `AuditRecord{action:"tls-insecure", result:"warning", detail:"InsecureSkipVerify…"}`（store 侧经 `auditAction` 折算为 `ldap/tls-insecure`，result `warning` 原样落 audit.jsonl；无凭据）。
- `dial.go`：`dialProfile` 签名改为 `(ctx, profile, target, bindSecrets)`；KDC 88 回退移植（回退前打 NOTICE 日志，不含凭据）。

## 4. 任务 3：manifest / 前端核对

- **manifest.json 无需改动**：L-A 已落 26 字段（含 krb_credential_type/krb_realm/krb_kdc_host/krb_kdc_port/krb_keytab_path/krb_ccache_path/krb5_conf_path/krb_password）；visible_when 二级联动核对无误——`krb_keytab_path`/`krb_ccache_path`/`krb_password` 按 `krb_credential_type` 联动（credential_type 自身仅在 `auth_type=kerberos` 时可见，故无需双条件）；七语 label 齐全。
- **auth_type 六种进阶值**（kerberos/ntlm/ntlm_hash/digest_md5/external）从 M1 的「显式报 `not supported yet (planned for M3)`」全部变为真实现；单测 + smoke 双断言防回归（`assert_not_m3_stub`）。
- 前端零改动（scripts/**、frontend/** 只读边界；连接表单由宿主渲染 manifest 字段）。

## 5. 任务 4：验证记录（2026-08-28）

| 验证 | 结果 |
|---|---|
| `go build ./...` / `go vet ./...`（backend） | PASS |
| `go test -count=1 ./...`（4 包） | **全绿**：ldapconn（M1 用例零回归 + 本路新增 M3 用例）、ldapgssapi、lifecycle、store |
| 新增单测 `internal/ldapconn/auth_dial_m3_test.go`（约 20 表驱动/子测试） | 全绿：SASL host 解析、SPN 推导表、principal/realm 拆分、KDC 88 回退判定与 Profile 副本、krb5.conf 生成（模板字段/非 88 端口 udp_preference_limit/0600/DBX_PLUGIN_DATA_DIR/krb5 落点/清理）、configPath 优先级、GSSAPI 客户端构造错误面（缺密码/keytab/ccache）、GSSAPI 选项（integrity/auth-conf/mutual）、lifecycle krb_* 接线与缺省（credentialType=password、KDCPort=88、Realm 大写）、ntlm/ntlm_hash/digest_md5/simple 参数错误面、kerberos 非桩断言、InsecureSkipVerify TLS 配置、tls-insecure 审计四分支 |
| 容器 smoke S1-S10（`smoke_container.py`，明文 1389） | **10/10 PASS，零回归**，容器已 down -v |
| M3/TLS smoke（`smoke_auth_test.py`） | **本地二进制与打包态二进制各一轮：9 PASS + 1 SKIP（A2，服务器无 DIGEST-MD5 机制），0 FAIL** |
| `PATH="/tmp/go-shim:$PATH" bash scripts/build.sh` | 全绿：前端 typecheck+vitest+vite → ui/index.html → backend go build → `dbx-plugin package .` |
| 打包产物 | `dist/io.dbx.ldap-0.1.0-darwin-arm64.dbxp`（5,912,056 B；M1 为 5.15 MB，增量来自 gokrb5 依赖链），unsigned review candidate；解包核验 manifest.json + bin/darwin-arm64/dbx-plugin-ldap + ui/index.html + assets + checksums.json，包内二进制实跑 TLS/M3 smoke 通过（见上） |

## 6. 改动清单（本路）

| 文件 | 改动 |
|---|---|
| `backend/internal/ldapconn/types.go` | +LDAPKerberosConfig/NormalizeLDAPKerberosConfig；Profile +Kerberos/SASLHost/SASLQoP/SASLMutualAuth；NormalizeProfile 扩展 |
| `backend/internal/ldapconn/dial.go` | bind 分发补全六种认证（external/digest_md5/ntlm/ntlm_hash/kerberos）+ `resolveLDAPSASLHost` + KDC 88 回退调用；`dialProfile`/`bindLDAPConnection` 签名带 ctx/logicalHost/bindSecrets |
| `backend/internal/ldapconn/auth_gssapi.go` | 新增：Kerberos bind 支撑全套（见 §3.1 对照表） |
| `backend/internal/ldapconn/service.go` | connEntry.secrets（bindSecrets）；NewProfileFromLifecycle 接 M3 字段；emitTLSInsecureAudit（tls_verify=false 审计 warning） |
| `backend/internal/ldapgssapi/{client,client_test}.go` | 新增：自外部参照原样搬运（实现 ldap.GSSAPIClient；integrity/confidentiality/mutualAuth） |
| `backend/internal/ldapconn/auth_dial_m3_test.go` | 新增：M3/TLS 单测（§5） |
| `backend/go.mod` / `go.sum` | +`github.com/jcmturner/gokrb5/v8 v8.4.4`（对齐参照基线）及传递依赖（aescts/dnsutils/gofork/goidentity/rpc） |
| `docker-compose.ldap-tls-test.yml` | 新增：LDAPS/StartTLS 容器变体（凭据仅经环境变量） |
| `scripts/smoke_auth_test.py` | 新增：M3/TLS smoke（自包含编排 + 就绪探测 + 临时自签证书 + SKIP 语义） |
| `backend/bin/dbx-plugin-ldap`、`dist/*.dbxp` | 重建/重打包（产物） |

**scripts/**、frontend/** 其余文件零改动**；无 git commit/push。

## 7. 遗留

1. **真机 Kerberos 集成**（实施文档 §8 的 `ldap-gssapi-test.yml` + `smoke_gssapi_test.py` 全绿 DoD）：容器无 KDC，本路以「死 KDC 网络错误 + 单测」验证到协议客户端边界；需要真实 KDC（如 freeipa/krb5-kdc 容器或域控）+ keytab/ccache 才能验证 AS_REQ/Ticket 全链路。已具备：SPN 推导、krb5.conf 生成、DisablePAFXFAST、integrity/confidentiality/mutualAuth 选项、KDC 88 回退均就位。
2. **真机 NTLM/NTLM-hash 集成**：需 AD 或 Samba4 DC；本路已验证 go-ldap NTLMSSP 调用路径与参数面（容器内 OpenLDAP 拒绝 NTLMSSP mech 属预期）。
3. **DIGEST-MD5 容器验证**：bitnami/openldap 的 cyrus-sasl 无 DIGEST-MD5 机制；需要带 `cyrus-sasl-digestmd5` 模块的 OpenLDAP 镜像（或 osixia/openldap）方可实跑成功路径。SASL host 解析已单测覆盖。
4. **external 的 ldapi 成功路径**：需 sidecar 与 slapd 同机共享 unix socket（DBX 宿主形态下 = 宿主本机 slapd），当前以 ExternalBind 真实调用 + 服务器拒绝为证据；后续如宿主有本机 slapd（`ldapi://`）可补成功场景。
5. **宿主端到端 / 浏览器截图**：沿袭 PROGRESS-D 遗留 1-2（.dbxp 未装测试宿主）。
6. **manifest 潜在增补**（未做，留决策）：参照基线尚有 SPN 覆盖（ExplicitSPN/ServiceName）、SASL QoP、mutualAuth、krb_username 等字段；本仓后端已预留内部接线点（`sasl_qop`/`sasl_mutual_auth`/`sasl_host`/`krb_username` config key），如宿主表单需要可按 §4 格式补 manifest+七语。

## 8. 阻塞

无。全部计划内验证完成：单测/回归/smoke/打包全绿，容器与临时产物（自签证书目录、解包目录、临时密码变量）已清理，无凭据落盘。
