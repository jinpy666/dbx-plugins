# P-LDAP 路进度记录（dbx-ldap-plugin 成熟化：manifest 决策落地 + UI 验证打磨 + 性能 + ldapi）

> 记录人：P-LDAP（dbx-ldap-plugin 成熟化增强）。
> 日期：2026-08-29。工具链：go 1.27.0（darwin/arm64）/ node 22.21.0 / pnpm 10 / Docker 29.4.0（OrbStack）/ dbx-plugin CLI 0.1.0（经 /tmp/go-shim 剥 GOWORK）/ python3.11。
> 凭据红线：smoke 容器密码全程随机生成、仅经环境变量（`LDAP_ADMIN_PASSWORD`），未写入任何文件/日志/报告；SASL EXTERNAL 场景零凭据上线；本路无新增凭据面。
> 前序：M1（L-A~L-D）+ D（TLS 容器）+ X-C（M3 认证）已完成；基线 go 四包全绿、S1-S10 10/10、TLS T1-T5 + M3 9/1/0、前端 68/68、出包 5.91MB。

## 1. 交付总览

| 交付 | 状态 |
|---|---|
| 任务 1：manifest 预留字段暴露决策落地（X-C 遗留 6） | **完成**（4 字段 + 七语 + lifecycle 接线单测 + manifest 契约守卫测试） |
| 任务 2：前端 mock 桥 + 浏览器截图验证 + UI 打磨 | **完成**（新增 mock.html/mockDbxHost.ts；12 张截图入 docs/screenshots/；修复 3 处缺陷） |
| 任务 3：性能验证与优化 | **完成**（千级条目分页 23ms、DN 树并发去重修复、schema 缓存命中证实） |
| 任务 4：ldapi:// Unix socket 支持 | **完成**（真机容器验证 3/3 PASS + 可复跑 smoke_ldapi_test.py；关闭 X-C 遗留 4 的 external/ldapi 成功路径） |
| 任务 5：全量验证 | **全绿**（go 四包 + pnpm 68 + S1-S10 10/10 + TLS/M3 9/1/0 + ldapi 3/3 + 出包 5,913,354 B） |
| 禁 git commit/push / 无新依赖 / 七语齐全 | **遵守**（依赖零变更；文案七语经 i18n.spec 校验） |

## 2. 任务 1：manifest 预留字段暴露（按消费方接线）

### 2.1 字段决策与映射

后端预留点核对结果（`service.go NewProfileFromLifecycle` 已接通全部 4 个 config key，无需补实现）：

| manifest 字段 | 类型/默认 | visible_when | 后端消费点 | 说明 |
|---|---|---|---|---|
| `sasl_host` | text，空 | `auth_type ∈ [digest_md5]` | `dial.go` `resolveLDAPSASLHost` → `MD5Bind` | DIGEST-MD5 SASL host 覆盖，缺省用 URL 逻辑主机 |
| `krb_username` | text，空 | `auth_type ∈ [kerberos]` | `LDAPKerberosConfig.Username` → `ldapKerberosPrincipalValues` | Kerberos 用户名覆盖（不含 Realm），缺省回落 `username` |
| `sasl_qop` | select auth/auth-int/auth-conf，默认 auth | `auth_type ∈ [kerberos]` | `auth_gssapi.go` `ldapGSSAPIClientOptions` | GSSAPI 保护质量 |
| `sasl_mutual_auth` | boolean，默认 false | `auth_type ∈ [kerberos]` | `ldapGSSAPIClientOptions.MutualAuth` | GSSAPI 相互认证开关 |

> **与任务字面要求的偏差说明**：任务原文为「digest_md5 的 sasl_qop/sasl_mutual_auth」。核对后按消费方暴露——go-ldap 的 `MD5Bind(host, username, password)` 无 security layer/QoP 协商（tiny-rdm 同构：其 UI 虽对 digest_md5 展示 QoP 面板，但 `conn.MD5Bind` 同样不消费 qop），把 `sasl_qop`/`sasl_mutual_auth` 挂到 digest_md5 会是零效果的死字段；二者真正生效面是 GSSAPI（kerberos），故 `visible_when` 绑 kerberos。`sasl_host` 为 digest_md5 实消费，绑 digest_md5。已在 manifest description 与本文档注明。

### 2.2 七语与测试

- manifest.json：4 字段插入 `krb5_conf_path` 与 `krb_password` 之间；`localizations` 七块（en/zh-CN/zh-TW/es/it/ja/pt-BR）label+description+options 齐全（26→30 字段）。
- `backend/internal/ldapconn/auth_dial_m3_test.go`：`TestNewProfileFromLifecycleM3Fields` 扩展 `sasl_host`/`krb_username` 接线断言。
- 新增 `backend/internal/ldapconn/manifest_contract_test.go`：契约守卫——4 字段存在、binding=config、visible_when 与消费方一致、sasl_qop 三选项、mutual 默认 false、七语 label/options 缺一即 FAIL。

## 3. 任务 2：前端 mock 桥与浏览器验证

### 3.1 新增 mock 基建（无新依赖）

| 文件 | 说明 |
|---|---|
| `frontend/mock.html` | 照 ssh-sftp mock.html 模式的 visual fixture 入口 |
| `frontend/src/mockDbxHost.ts` | `window.dbxPlugin` 桥 mock：内存目录（dc=demo,dc=dbx：ou=people 下 1000 用户、ou=groups/services、根 DSE、RFC 4512 schema 文本）、RFC 4515 过滤器求值器、全量 `ldap/*` 方法（含 presets localStorage 持久化）；URL 参数 `theme/locale/err/noconn/ro` 注入错误态与只读态 |
| `frontend/package.json` | 补 `"dev": "vite"` 脚本（原先只有 build/typecheck/test） |

### 3.2 截图清单（docs/screenshots/，浏览器 1440×900）

| # | 文件 | 验证点 |
|---|---|---|
| 01 | workbench-initial.png | 初始布局：工具栏/DN 树/搜索表单/空态 |
| 02 | results-1000-entries.png | 千级条目结果表分页（1008 条 21 页） |
| 03 | context-menu.png | 右键菜单 7 项（只读项正确禁用在 09 验证） |
| 04 | entry-editor-form.png | 条目编辑器表单模式（cn=admins 属性行） |
| 05 | schema-panel.png | Schema 面板（26 属性类型/12 对象类 + 过滤 + 强刷） |
| 06 | error-tree-conn-failed.png | 连接失败：树区错误态 |
| 07 | error-banner-search-failed.png | 搜索失败 showError 横幅 |
| 08 | error-init-noconn.png | 无 connectionId 的初始化错误态 |
| 09 | readonly-light-contextmenu.png | 只读连接 + 浅色主题：写操作三项禁用 |
| 10 | connections-panel.png | 连接状态面板（已连接/最近使用） |
| 11 | tree-keyword-filter.png | 树关键字远程过滤（防抖 300ms） |
| 12 | add-entry-via-ldif.png | LDIF 模式新增条目 + 树自动刷新（cn=metrics） |

### 3.3 交互走查结论与修复

| 走查项 | 结论 |
|---|---|
| DN 树懒展开 | PASS：`ou=people` 展开 500 子节点（服务端 sizeLimit 500）渲染 <20ms |
| 右键菜单 | PASS：7 项动作全通（在此搜索联动表单 Base DN、查看/编辑、新增、重命名、删除、导出、复制 DN）；只读模式写三项禁用 |
| 搜索表单联动 | PASS：context menu「在此搜索」回填 Base DN；预设保存/应用/删除全通 |
| 结果表分页 | PASS：1008 条 21 页，翻页仅渲染当前页 50 行 |
| EntryEditor LDIF 双向 | PASS：表单→LDIF 生成、LDIF 编辑→表单解析、非法行内联报错（`L6: malformed line`）、保存走 `entry/modify` 后树刷新 |
| Schema 面板 | PASS：缓存命中（见 §4） |
| 错误态 | PASS：init 错误/树错误/横幅三态文案齐全 |
| 主题 | PASS：dark/light 两态、只读徽章 |

**当场修复的 UI 缺陷（3 处）**：

1. `SearchForm.vue` 删除预设图标按钮的 title 误用通知文案 `search.presetRemoved`（「预设已删除」）→ 新增动作 key `search.presetRemove`（七语）并补 `aria-label`。
2. `SearchForm.vue` 仅返回类型下拉硬编码 "No"/"Yes" → 新增 `search.typesOnlyYes/typesOnlyNo` 七语。
3. `DnTree.vue` 同一节点快速连点触发并发重复请求（实测 3 连点 3 次请求）→ `toggleNode`/`loadRoot` 加 loading 守卫，修复后 3 连点 1 次请求（§4 数据）。

文案七语：i18n.ts 七语各 +3 key；`i18n.spec.ts`（key 集合一致性）68/68 通过。

## 4. 任务 3：性能数据（mock 桥，OrbStack 本机）

| 场景 | 数据 | 结论 |
|---|---|---|
| 千级条目搜索+渲染（1008 条，sizeLimit 2000） | 搜索+首屏渲染 **23ms**（30 次取中位）；每页 50 行、共 21 页 | 分页已足够，无需虚拟滚动 |
| 翻页 | **<1ms**（仅 slice 当前页） | — |
| 排序（1008 条前端重排） | <30ms | — |
| DN 树展开（500 子节点拉取+渲染） | <20ms | 懒加载保持 scope=one |
| 同一节点并发去重 | 修复前 3 连点 = 3 次 `ldap/search`；修复后 = **1 次** | `toggleNode` loading 守卫 |
| 树关键字过滤 | 300ms 防抖 + `filterSequence` 竞态序号去重（既有实现核对通过） | — |
| schema 缓存命中 | 面板首开 `ldap/schema` 调用 1 次；关闭再开 **0 次新调用**（进程内 TTL 30min 缓存命中）；强刷按钮走 `refresh:true` 绕过 | 二次打开不重复拉取证实 |

## 5. 任务 4：ldapi:// Unix socket（真机）

### 5.1 结论：支持已存在，真机验证通过（非 SKIP）

- `dial.go` 对 `ldapi` scheme 原样透传 go-ldap `DialURL`（conn.go:162-167：URL Path 即 socket 路径，空时兜底 `/var/run/slapd/ldapi`）；网络层 D5 改造不触及 ldapi 分支。
- 新增单测 `internal/ldapconn/ldapi_dial_test.go`：本地假 unix 监听验证「scheme 解析 → AF_UNIX 拨号成功 → ExternalBind 在 socket 上发出（bind 等待响应超时）」。
- **真机容器验证**（Docker Desktop 的 socket bind-mount 宿主不可连——实测 OrbStack 返回 connect refused，故采用**容器内拓扑**）：
  1. sidecar 交叉编译为静态 linux/arm64（`CGO_ENABLED=0`，6.8MB）；
  2. `docker cp` 进 bitnami/openldap（slapd `-h ldapi:/// ldap://:1389/`）；
  3. `docker exec -i` 驱动 sidecar stdio JSONL。
- 实测结果（3/3 PASS）：`connection/test`（ldapi + auth_type=external）→ `success:true`（`auth=external; base dc=example,dc=org returned 1 entrie(s)`）；`connection/connect` + `ldap/search`（sub）→ count=6；`ldap/rootDse` → 属性非空。**该路径关闭 X-C 遗留 4（external 的 ldapi 成功路径此前仅有服务器拒绝证据）。**
- 新增可复跑脚本 `scripts/smoke_ldapi_test.py`（L1/L2/L3）：自包含编排（交叉编译→起容器→socket 稳定性门控——bitnami 入口分两阶段起 slapd，需三探针防瞬态→docker cp→`docker exec -i` 驱动→断言→清理），容器密码随机仅经环境变量、EXTERNAL 零凭据。

## 6. 任务 5：全量验证（2026-08-29）

| 验证 | 结果 |
|---|---|
| `go build` / `go vet` / `go test -count=1 ./...`（四包） | **全绿零回归**（ldapconn 含新增 manifest 契约 + ldapi 单测） |
| `pnpm typecheck` / `pnpm test` | typecheck 干净；**68/68**（i18n 七语一致性含新 key） |
| `pnpm build`（build.mjs → ui/index.html） | PASS（出包链路内执行） |
| `smoke_container.py`（S1-S10） | **10/10 PASS**，容器 down -v 清理 |
| `smoke_auth_test.py`（TLS T1-T5 + M3 A1-A5） | **9 PASS + 1 SKIP**（A2：bitnami 无 DIGEST-MD5 mech，沿袭既有 SKIP+原因），容器与临时证书清理 |
| `smoke_ldapi_test.py`（L1-L3） | **3/3 PASS**（新增），容器清理 |
| `PATH="/tmp/go-shim:$PATH" bash scripts/build.sh` | 全绿：前端→ui/index.html→backend→`dbx-plugin package .` |
| 打包产物 | `dist/io.dbx.ldap-0.1.0-darwin-arm64.dbxp`（**5,913,354 B**；基线 5,912,056 B，增量来自 manifest 4 字段/七语 + 前端修复 + 单测不进包），unsigned review candidate |

## 7. 改动清单（本路）

| 文件 | 改动 |
|---|---|
| `manifest.json` | +4 字段（sasl_host/krb_username/sasl_qop/sasl_mutual_auth）+ 七语 localizations（26→30） |
| `backend/internal/ldapconn/manifest_contract_test.go` | 新增：manifest 契约守卫测试 |
| `backend/internal/ldapconn/auth_dial_m3_test.go` | 扩展：sasl_host/krb_username lifecycle 接线断言 |
| `backend/internal/ldapconn/ldapi_dial_test.go` | 新增：ldapi unix socket 拨号单测 |
| `frontend/mock.html`、`frontend/src/mockDbxHost.ts` | 新增：visual fixture 宿主桥 |
| `frontend/package.json` | +`dev` script |
| `frontend/src/lib/i18n.ts` | 七语 +3 key（presetRemove/typesOnlyYes/typesOnlyNo） |
| `frontend/src/components/SearchForm.vue` | 修复删除预设 title/aria-label、typesOnly 选项 i18n |
| `frontend/src/components/DnTree.vue` | 修复节点并发重复请求（loading 守卫）与刷新连点去重 |
| `scripts/smoke_ldapi_test.py` | 新增：ldapi 真机 smoke（自包含编排） |
| `docs/screenshots/*.png`（12 张） | 浏览器验证留档 |
| `backend/bin/dbx-plugin-ldap`、`dist/*.dbxp` | 重建/重打包（产物） |

后端实现本体（dial/service/auth_gssapi/types）零改动——预留点核对全部已就位。无 git commit/push；依赖零新增。

## 8. 遗留（条件项，均非本路引入）

1. **KDC 真机集成**（smoke_gssapi_test.py 全绿 DoD）：需真实 KDC/域控；A5 已验证到协议客户端边界（沿袭 X-C 遗留 1）。
2. **NTLM 真机（AD/Samba4 DC）与 DIGEST-MD5 容器**（需带 cyrus-sasl-digestmd5 的镜像）：沿袭 X-C 遗留 2/3。
3. **宿主端到端（.dbxp 装测试宿主）**：连接表单 4 个新字段的宿主渲染效果未在真宿主验证（宿主按 manifest 渲染为通用表单控件，风险低）；沿袭 D 遗留 1-2。
4. **树子节点计数徽章**：`ou=people` 显示 500（服务端 sizeLimit 截断值）而非真实 1000——沿袭 tiny-rdm 语义（sizeLimit 500），如需精确计数需额外 `ldap/count` 能力，本路未做。
5. **mock 桥仅覆盖正/误注入**：未模拟权限拒绝（denied audit 事件）分支的截图，该分支代码路径与错误横幅共用 showError（07 已覆盖展示形态）。

## 9. 阻塞

无。

## 10. 真实 AD 只读验证 + 匿名 bind 修复（2026-08-31 追加）

背景：用真实企业 AD 域控（ldaps 636 / ldap 389，CORP 林）做只读冒烟；凭据
全程仅经环境变量注入（`LDAP_REAL_*`），未写入任何文件/日志/报告。

### 10.1 交付

| 交付 | 状态 |
|---|---|
| `scripts/smoke_real_test.py`（R1-R10 只读真机 smoke） | 新增：connection/test、rootDse、base 搜索、分页聚合、count、entry/get、schema、白名单、坏过滤器、断连；连接默认 `read_only=true` 防御；凭据缺失/不可达按 SKIP 语义 |
| anonymous bind AD 兼容修复（`dial.go`） | 完成：原实现对 anonymous 跳过 bind，AD 拒绝未绑定连接上的目录操作（Operations Error, 000004DC）；改为显式 RFC 4513 匿名 simple bind（空 DN + 空密码，`AllowEmptyPassword: true`） |
| 真实 AD 复测（匿名路径） | connection/test 成功；rootDse 返回全部 5 个 namingContexts（与 Studio 基线一致）；匿名读目录树被 AD 正确拒绝（预期，AD 匿名仅开放 RootDSE） |

### 10.2 回归验证

| 验证 | 结果 |
|---|---|
| `go vet` + `go test ./...`（四包） | 全绿 |
| `smoke_container.py`（S1-S10） | 11/11 PASS，容器 down -v 清理 |
| `smoke_auth_test.py`（T1-T5 + A1-A5） | 9 PASS + 1 SKIP（A2 沿袭：容器无 DIGEST-MD5 mech） |

### 10.3 阻塞

真实服务器凭据绑定被 AD 拒绝（result 49 / data 52e；sidecar 与系统
`ldapsearch` 双通道独立复现，传输层排除）。判定为凭据本身被拒（口令变更/
失效或抄录失真），非插件缺陷；`LDAP_REAL_URL_PLAIN` 明文 389 通道同样 52e。
待有效凭据后重跑 R1-R10 凭据场景（脚本与场景已就绪，环境变量驱动）。
