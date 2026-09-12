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

> **与任务字面要求的偏差说明**：任务原文为「digest_md5 的 sasl_qop/sasl_mutual_auth」。核对后按消费方暴露——go-ldap 的 `MD5Bind(host, username, password)` 无 security layer/QoP 协商（外部参照实现同构：其 UI 虽对 digest_md5 展示 QoP 面板，但 `conn.MD5Bind` 同样不消费 qop），把 `sasl_qop`/`sasl_mutual_auth` 挂到 digest_md5 会是零效果的死字段；二者真正生效面是 GSSAPI（kerberos），故 `visible_when` 绑 kerberos。`sasl_host` 为 digest_md5 实消费，绑 digest_md5。已在 manifest description 与本文档注明。

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
4. **树子节点计数徽章**：`ou=people` 显示 500（服务端 sizeLimit 截断值）而非真实 1000——沿袭初版语义（sizeLimit 500），如需精确计数需额外 `ldap/count` 能力，本路未做。
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

## 11. mock 桥补齐：ldap/count fixture + denied audit 分支（2026-09-01 后台 agent 轮）

后台 agent 执行轮：最终汇报输出乱码（不可读），改动本体完整有效（仅
`ldap/frontend/src/mockDbxHost.ts`，+56 行），由主 agent 补齐验证与本文档。
对应 §8 遗留 5（mock 桥 denied audit 分支）与遗留 4 的 fixture 侧补齐
（`ldap/count` 后端/前端接入为既有实现，fixture 此前未覆盖）：

1. **mock 桥新增 `ldap/count`**：`countChildren`——scope=one 语义（baseDN
   直接子条目、过滤 `filter`），上限 5000，超出折算 `truncated:true`；err
   注入沿用 `?err=1`。DnTree 懒加载徽章从此走精确计数（此前 mock 无该方法
   徽章降级为 search 截断值 500）。
2. **denied audit fixture**：`denyWrite`——readOnly 下写操作先 emit
   `ldap/audit`（`action:"write-policy", result:"denied"`，与后端
   AuditRecord 语义一致）再抛业务错误；`ldap/entry/{add,modify,delete,
   modifyDn}` 四个写路径统一接入。

### 11.1 验证（主 agent 复核）

| 套件 | 结果 |
| --- | --- |
| ldap/frontend typecheck + vitest | 过 / **142 绿**（8 spec） |
| ldap/frontend build | 过（产物写 ui/index.html） |
| backend `go vet ./...` + `go test ./...` | 全绿（四包，四包均 ok） |

浏览器验证（visual fixture @ vite 5182，截图
`docs/screenshots/count-badge-mock-bridge-round-p11.png`）：展开 ou=people
后徽章显示精确 **1000 个子条目**（遗留 4 所述 500 截断值不复现）。

说明：ro=1 模式下写操作在 UI 层即被禁用，denied audit 分支按设计不可经
浏览器 UI 触达（需从 invoke 层触发）；fixture 代码路径与后端
write-policy 分支语义一一对应，真宿主端到端验证沿袭遗留 3。

### 11.2 遗留 / 下一轮候选

- 沿袭 §8 遗留 1/2/3（KDC/NTLM/DIGEST-MD5 真机、宿主 e2e）不变；
- 新候选：搜索结果虚拟滚动（大目录 1000 条渲染体验）、预设持久化接后端
  store、audit 面板可视化（denied 事件已有数据面）。

## 12. audit 面板可视化收尾（2026-09-02 收尾 agent 轮）

§11.2 候选「audit 面板可视化」的落地与收尾。上一轮 agent 中途断线，留下
半成品（`AuditFeedPanel.vue` 缺 SFC 开标签、i18n 仅 en/zh-CN 两语、无单测），
本轮补齐至绿并完成浏览器验证。本轮无后端改动（`git diff` 复核）。

### 12.1 改动清单

1. **新增 `frontend/src/lib/auditFeed.ts`**（上轮半成品，本轮复核通过）：
   `ldap/audit` 事件流数据面纯函数——`parseAuditEvent`（AuditRecord JSON 同面
   解析，缺省兜底、未知 result 折算 error）、`pushAuditItem`（最新插头、上限
   100 裁尾）、`formatAuditTime`（同天 HH:MM:SS、跨天加 MM-DD 前缀）、
   `normalizeAuditResult`。
2. **新增 `frontend/src/components/AuditFeedPanel.vue`**（上轮半成品 + 本轮
   修复）：纯展示组件，头部常驻事件总数 + denied/error 计数徽标，列表折叠
   可展开，denied/error 到达自动展开一次，清空由父级处理。**修复**：补缺失
   的 `<script setup lang="ts">` 开标签（typecheck 23 个 TS2339 的根因）。
3. **新增 `frontend/src/lib/auditFeed.spec.ts`**（本轮）：8 例覆盖
   parse/push/format 的兜底、裁剪、跨天逻辑。
4. **改动 `frontend/src/App.vue`**（上轮完成）：`handleEvent` 接
   `parseAuditEvent`/`pushAuditItem` 数据面（横幅/通知即时反馈保留），
   `<AuditFeedPanel>` 接线 + `clearAuditFeed`。
5. **改动 `frontend/src/lib/i18n.ts`**：`audit.*` 文案块上轮仅 en/zh-CN，
   本轮补齐 zh-TW/es/it/ja/pt-BR 五语（七语齐）。

### 12.2 验证证据

| 套件 | 结果 |
| --- | --- |
| ldap/frontend `npm run typecheck` | 0 错 |
| ldap/frontend `npm run test` | **150 绿**（9 spec，上轮 142 + auditFeed 8） |
| ldap/frontend `npm run build` | 过（产物写 ui/index.html） |
| backend `go vet ./...` + `go test ./...` | 全绿（四包 ok，未动后端） |

浏览器验证（visual fixture @ vite 5182，`?ro=1`；截图
`docs/screenshots/audit-feed-panel-denied-ro1.png`）：

- 空态：无事件时面板不渲染（`v-if="hasEvents || expanded"`）；
- 触达方式：ro=1 下写操作 UI 层已禁用，denied 事件经 **invoke 层**触发——
  依次调 `ldap/entry/{delete,add,modify,modifyDn}`，四者均先 emit
  `ldap/audit denied` 再抛 "connection is read-only (fixture)"；
- 渲染：面板自动展开，摘要「4 条事件 · 4 条被拒绝」，4 条 denied
  （write-policy + 目标 DN）最新在顶，同时 showError 横幅可见；
- 交互：折叠按钮收起列表（aria-expanded=false）、清空按钮后面板回到隐藏态；
- 控制台仅 favicon 404（既有无害噪音）。

### 12.3 遗留 / 剩余风险

- 沿袭 §8 遗留 1/2/3（KDC/NTLM/DIGEST-MD5 真机、宿主 e2e）不变；denied
  audit 的真宿主链路（sidecar emitter.Event → 宿主桥 → 面板）仍待宿主 e2e；
- 面板仅内存态（刷新即清空，上限 100 条），按设计不持久化（audit.jsonl 才是
  权威日志）；ok 事件不触发自动展开，多 ok 后需手动展开查看；
- 搜索结果虚拟滚动、预设持久化接后端 store 两个候选沿袭至下轮。

## 13. 树/搜索虚拟滚动（§12 候选一，2026-09-02）

第三轮 agent 因配额超限中断，实现主体完整；其遗留 1 个测试期望值笔误
（computeWindow 顶部 overscan 语义：上方 overscan 被 clamp、end 为排他索引，
正确期望 16 而非 24），主 agent 修正后全绿并补齐验证与本文档。

**实现**（纯前端，零新依赖）：
1. `lib/virtualScroll.ts`：固定行高虚拟窗口纯函数 `computeWindow`
   （scrollTop/viewport/total/rowHeight/overscan → [start,end) 排他窗口，
   空/退化输入回空窗、越界 clamp 不倒置）+ 9 例单测；
2. `components/VirtualList.vue`：通用虚拟列表容器（spacer 撑高 + 可视窗
   渲染 + `reset-key` 滚动复位），复用于树与过滤结果两处；
3. `DnTree.vue`：`flattenDnTree` 扁平投影可见行 → VirtualList 窗口化
   （行高 28px 与 CSS 一致）；`TreeBranch.vue` 从递归嵌套渲染改为单行
   渲染器（depth prop → 左缩进），展开/选中/右键语义不变（状态仍在
   node 对象上）；过滤结果列表同步接入（`reset-key=filterKeyword`）；
4. `lib/dnTree.ts` 新增 `flattenDnTree` + 5 例单测；i18n 无新键（无新
   文案）。

**验证**：typecheck 0 错；vitest **164 绿**（150→164：virtualScroll 9 +
dnTree 5）；build 过；后端未动。浏览器实测（5182 fixture，截图
`docs/screenshots/tree-virtual-scroll-round-p13.png`）：展开 ou=people
（500 sizeLimit 子条目，扁平树 504 行、spacer 14112px）仅渲染 **80 个 DOM
行**；scrollTop=12000 后窗口移动至 uid=user0462 区段、回收至 **48 行**；
点击 uid=user0420 选中态正确（`.selected` 同步）。

**剩余风险**：固定行高假设（行高 28px 由 CSS 常量约定，改样式需同步
TREE_ROW_HEIGHT）；键盘导航（上下键）沿用以 DOM focus 为准，依赖窗口内
渲染行为未单测；预设持久化候选沿袭下轮。

## 主题令牌桥（2026-09-05）

- 接入 `shared/frontend/themeSync.ts`：`main.ts` 挂载前 `installHostThemeBridge()`，
  插件变量桥接宿主 `--color-*` 令牌——首绘即命中宿主主题（不再等 init 后 JS 回写），
  主题切换自动跟随，primary/radius/字体纳入同步面。宿主无令牌（mock/旧宿主）回退
  暗色规范值，行为不变。
- 验证：`vue-tsc` 0 错；`vitest run` 15 文件 193 用例全绿（含新增
  `themeSync.spec.ts` 薄 spec）；v0.1.32 发版。

## 白色主题配色标准化（2026-09-05 第二轮）

四插件联合审查白色主题配色错误，语义令牌与明暗分支在
`shared/frontend/themeSync.ts` 单点收敛（详见该文件与 shared/frontend/README）。

- ldap 本轮替换：`.state-dot.connected`（#10b981 → `--success`）、modal 遮罩
  （亮色 #000/50% 偏重、暗色 92% 背景 mix → 统一 `--overlay`）、图标 dark
  变体双属性化（`data-theme` + `data-dbx-theme`）。
- 验证：`vue-tsc` 0 错；`vitest run` 15 文件 194 用例全绿（themeSync 薄 spec
  增补语义令牌/遮罩/light 回退断言）。无新增文案，七语不受影响。

## 弹窗/右键菜单键盘可访问性 + 反馈保真（2026-09-05 第三轮）

§11.2/§13 沿袭候选之外的交互专业化轮：对齐 kafka 已有的弹窗键盘语义
（kafkaModel.decideModalKeydown 家族统一），修复反馈失真细节。

### 改动清单

| 文件 | 改动 |
|---|---|
| `frontend/src/lib/modal.ts` | 新增：`FOCUSABLE_SELECTOR`/`focusableElements`/`nextFocusIndex`/`decideModalKeydown` 纯函数（与 kafka 同语义）+ `useModalA11y` composable（open 时记录触发元素→挂 window keydown→nextTick 焦点进首控件；关闭时摘监听+焦点归还） |
| `frontend/src/lib/modal.spec.ts` | 新增 7 例：Esc close/Tab 回绕/空容器/焦点在外的进入方向 + focusableElements 真实 DOM 选择器覆盖（disabled/hidden/tabindex=-1 排除） |
| 5 个弹窗组件 | EntryEditor/DeleteEntry/ModifyDn/Schema/Connections 统一接 `useModalA11y`；EntryEditor `allowClose` 否决脏态 Esc（`有未保存的修改` 提示在场，防误丢），Delete/ModifyDn 提交在途否决 Esc；ModifyDn RDN 输入框 Enter 直接触发确认 |
| `DnTree.vue` | 右键菜单：渲染后按实际尺寸夹回视口（右/底缘不裁切）、`role=menu`+`menuitem`、容器聚焦 + ↑/↓ 项间移动（复用 `nextFocusIndex`）、Esc 关闭并焦点归还触发节点 |
| `App.vue` | copyDn 修复：桥缺失（可选链静默假成功）或写入失败时如实反馈——execCommand 兜底，双失败提示 `copyFailed`（此前 catch 也提示「已复制」）；错误横幅 ✕ 文本 → X 图标 + `aria-label=close` + `role=alert`；notice `role=status` |
| `ResultTable.vue` | pager prev/next 按钮补 `title`+`aria-label`（prev 原 title 误用页码信息） |
| `lib/i18n.ts` | 七语各 +3 key：`copyFailed`、`result.prevPage`、`result.nextPage`（21 处） |

### 验证（typecheck/test/build + 浏览器实机）

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` | 0 错 |
| `pnpm test` | **201 绿**（16 文件；194→201：modal.spec 7） |
| `pnpm build` | 过（产物写 ui/index.html） |

浏览器实机（visual fixture @ vite 5182，1440×900，明暗两主题走查；截图
`docs/screenshots/ui-modal-a11y-{light-workbench,light-results-contextmenu,light-editor,dark-results-contextmenu}-p14.png`，gitignore 不入库）：

- 右键菜单：clientX/Y=(1430,890) 打开 → 实测夹回 (1236,669)，200×227 完整可见（明暗两主题同验）；`role=menu` 成立；容器聚焦后 ArrowDown→首项「在此搜索」、ArrowUp 回绕；Esc 关闭 + 焦点归还 ou=people 节点按钮（真实点击路径验证）。
- 编辑器弹窗：打开焦点落在首控件（header 关闭按钮，与 kafka 一致）；Shift+Tab 从首控件回绕到 footer 主按钮（陷阱成立）；清洁查看态 Esc 关闭；修改属性值出现「有未保存的修改」后 Esc 被否决（弹窗保持），「取消」仍可显式关闭。
- 连接面板：打开焦点进面板首控件；Esc 关闭后焦点归还工具栏「连接」按钮。
- 复制 DN：notice「已复制」带 `role=status`。
- 亮色主题走查：工作台/结果表(500 条 10 页)/右键菜单/编辑器弹窗对比度全部正常，遮罩 40% 黑统一。

### 说明与遗留

- 后端零改动（本轮纯前端）；`decideModalKeydown` 与 kafka 为同语义复制件，
  如需收敛可在 shared/frontend 提升为公共层（kafka 侧迁移另立任务）。
- Esc 脏态否决无额外提示文案（footer「有未保存的修改」常驻提示已表达原因）。
- 沿袭遗留：§8 遗留 1/2/3（真机/宿主 e2e）不变；树箭头键导航（依赖虚拟窗口）
  仍沿袭 §13 剩余风险。

## UI 扫描 P1 修复：遮罩 dirty 守卫 + 树懒加载截断可见化（2026-09-06）

第 1 轮场景化 UI 扫描（`docs/UI_SCAN_FINDINGS.zh-CN.md`）两条 P1 全部修复并
浏览器复验通过；顺带把工作区基线上 6 个既有失败用例归零。P2 × 12 未动
（不在本轮范围）。

### 改动清单

| 文件 | 改动 |
|---|---|
| `frontend/src/lib/modal.ts` | ① 新增 `decideBackdropClose` 纯决策：遮罩点击与 Esc 共用同一条 `allowClose` 守卫（P1-1 根因：`@click.self` 直关绕过 dirty 否决丢改动）；② `useModalA11y` watch 补 `immediate: true`——以 open=true 直接挂载的弹窗此前整场没有 Esc/Tab 监听（既有测试失败的根因） |
| `frontend/src/components/EntryEditorDialog.vue` | `canRequestClose()` 提为具名守卫，遮罩 `@click.self` 改走 `onBackdropClick` → `decideBackdropClose`：dirty/提交在途静默否决（footer「有未保存的修改」在场），✕/取消仍为显式放弃，干净态遮罩直关不变 |
| `frontend/src/lib/dnTree.ts` | `DnTreeNode` +`truncated` 标记；新增 `TREE_FETCH_PAGE=500`、`isFetchTruncated`、`nextFetchLimit`、`childBadgeText` 纯函数（P1-2：截断必须可见化，徽标不得背书假完整性） |
| `frontend/src/components/DnTree.vue` | `fetchChildren` 参数化单页上限并返回截断标记；根加载/懒展开记录 `truncated`；新增 `loadMore`（按已加载数+一页重取整体替换，`preserveExpansion` 保留子节点展开状态）；`refreshChildCount` 用 ldap/count 精确总数纠正截断标记（含「恰好整页」边界误判）；`invalidate` 重置 `truncated` |
| `frontend/src/components/TreeBranch.vue` | 截断节点徽标变 `button.tree-badge--truncated`：文案「已加载数+」（`childBadgeText`），title/aria-label 给「已加载 x / 共 y，点击加载更多」（count 未回降级「已加载前 x 个…（已截断）」），点击 emit `loadMore`；未截断仍为精确数 span |
| `frontend/src/style.css` | `button.tree-badge--truncated` 主色可点击样式 |
| `frontend/src/lib/i18n.ts` | 七语各 +2 key：`tree.childCountTruncated`、`tree.childCountTruncatedUnknown`（14 处） |
| 既有失败归零 | `AuditFeedPanel.vue` 自动展开改为「列表含 denied 即展开（含初始挂载/后续更新）」，error 只徽标高亮（修复清空列表后面板整体消失）；`EntryEditorDialog.spec.ts` 补 mock 计数重置；`SchemaPanel.spec.ts` 过滤断言对齐实际子串语义（"userid" 不含 "uid"）+ resolveSchema 竞态改 `flushPromises` |

### 测试

| 文件 | 用例 |
|---|---|
| `lib/modal.spec.ts` | +2：`decideBackdropClose` 放行/否决（与 Esc 同守卫语义） |
| `components/EntryEditorDialog.spec.ts` | +3：dirty 遮罩否决且修改保留（撤回后恢复关闭）、新增态半填 RDN 遮罩否决、干净态遮罩直关；另补 `beforeEach` mock 重置 |
| `lib/dnTree.spec.ts` | +5：单页常量、截断判定、续载上限步进、截断徽标「500+」不背书精确总数、全量后恢复精确数 |
| `components/TreeBranch.spec.ts` | 新增 4 例：精确数 span 徽标、截断「500+」徽标 title 含 loaded/total 且点击 emit loadMore、count 未回降级 title、disabled 不发续载 |

### 验证（typecheck/test + 浏览器实机复验）

- `pnpm typecheck` 0 错；`pnpm test` **22 文件 265 用例全绿**（基线 246 + 新增
  14 − 失败归零；本轮起步时工作区既有 6 例失败，见上表归零项）。
- 浏览器复验（vite 5292 + playwright-core 1.63 + 系统 Chrome headless，
  mock.html，1280×900，11/11 断言通过；截图即验即删未入库）：
  - P1-1：编辑 ou=people 产生 dirty → 点遮罩 → 弹窗保持打开、值保留；
    Esc 否决不回退；✕ 显式关闭后重开为原值；干净态遮罩点击正常关闭。
  - P1-2：展开 ou=people → 徽标「500+」、title「已加载 500 / 共 1000 个子条目，
    点击加载更多」→ 点击续载 → 徽标恢复精确「1000」、截断标记清除，树底部
    可达 uid=user0999。
  - 复验中发现并修正一个边界 bug：总条数恰好等于续载上限时
    `isFetchTruncated(fetched=1000, limit=1000)` 误标截断（徽标停在「1000+」），
    由 `refreshChildCount` 以 ldap/count 精确总数纠偏。

### 说明与遗留

- 遮罩 dirty 否决为静默否决（与 Esc 一致），无新增弹窗文案；七语仅树截断 +2 key。
- 同类隐患备忘：ModifyDn/Delete/Schema/Connections 四个弹窗遮罩仍是
  `@click.self` 直关，均无 dirty 态（ModifyDn 仅提交在途否决 Esc，风险窗口极小），
  后续如收敛按 optional 跟进。
- 沿袭遗留：§8 遗留 1/2/3 与 §13 树箭头键导航不变；P2 × 12 见扫描报告待后续轮次。

## UI 扫描 P2 修复轮：12 条打磨项全部收口（2026-09-06）

扫描报告 `docs/UI_SCAN_FINDINGS.zh-CN.md` P2-1 ～ P2-12 全部修复并浏览器复验
通过（playwright-core 1.63 + 系统 Chrome headless，mock.html，26/26 断言通过，
截图即验即删未入库）。`pnpm typecheck` 0 错；`pnpm test` 23 文件 **279 用例
全绿**（265 → 279，+15 新增 −1 SchemaPanel 断言改写并入新例）。

### 修复清单

| 项 | 方式与文件 |
| --- | --- |
| P2-1 connection lost 英文透传 | `ldapErrors.ts` network 规则补 `connection lost\|closed`；title 悬停仍留原文供排查 |
| P2-2 noconn 态自相矛盾 | `App.vue` spinner `!ready && !initError`；identity 降级 `connectionPlaceholder`（"暂无活跃连接"） |
| P2-3 空结果两态 | `App.vue hasSearched` → `ResultTable` 按 searched 分流 `result.emptyNoMatch` |
| P2-4 Schema 无匹配误显"为空" | `SchemaPanel.vue` 空列按关键字分流 `schema.noMatch` |
| P2-5 非法过滤器搜索按钮不联动 | `SearchForm.vue` 按钮 `:disabled` 加 `!filterValid` + title 提示 |
| P2-6 RDN 无客户端校验 | `EntryEditorDialog.vue` 新增态用 `isLikelyRdn` 预检：行内 `editor.rdnInvalid` + 保存禁用 |
| P2-7 树选中静默改写 Base DN | `SearchForm.applyBaseDn(next, highlight=true)`：变化时 1.6s `.base-dn-flash` 高亮 + `search.baseFollowed` title；初始化/自动定位传 false 不触发 |
| P2-8 错误横幅遮挡表单 | 横幅从 absolute 遮罩改布局流内（工具栏与主区之间），主区下移让位 |
| P2-9 light 工具栏染色偏重 | `toolbarStyle` 按 colorScheme 分档：light 5%/0.12、dark 10%/0.18（kafka 对齐） |
| P2-10 树无键盘导航 | `dnTree.ts nextTreeFocusIndex` 纯函数 + `DnTree` 容器 `@keydown`：↑/↓ 可见行间移动 + scrollIntoView，Enter 原生选中 |
| P2-11 删除弹层初始焦点 ✕ | `modal.ts useModalA11y` 新增 `initialFocus` 选项；DeleteEntryDialog 传 `"footer button"` 聚焦取消 |
| P2-12 mock favicon 404 | `mock.html` 内联 SVG data-icon（kafka 对齐） |
| 遮罩守卫家族收口（optional） | DeleteEntryDialog / ModifyDnDialog 遮罩接入 `decideBackdropClose`（与 Esc 同 allowClose）；Schema/Connections 无守卫语义、行为等价，保持 `@click.self` |

### 文案（七语 +5 key）

`connectionPlaceholder`、`result.emptyNoMatch`、`schema.noMatch`、
`editor.rdnInvalid`、`search.baseFollowed`（i18n.spec 七语 key 集合一致性守卫覆盖）。

### 测试（+15）

| 文件 | 用例 |
| --- | --- |
| `lib/ldapErrors.spec.ts` | +1：connection lost/closed 归 network 映射 |
| `lib/dnTree.spec.ts` | +4：nextTreeFocusIndex 相邻移动/边缘钳制/树外进入/非方向键不接管 |
| `components/SearchForm.spec.ts` | +2：非法过滤器禁用搜索按钮（title+恢复）、Base DN 跟随高亮（highlight=false 不触发） |
| `components/ResultTable.spec.ts` | 新增 3 例：未搜索/无匹配两态空文案 |
| `components/SchemaPanel.spec.ts` | 无匹配断言改写 + 1 例两态区分（zzz 无匹配 vs 空数据"Schema 为空"） |
| `components/EntryEditorDialog.spec.ts` | +1：三类非法 RDN 拦截（保存禁用+不发请求）、合法恢复 |
| `components/DeleteEntryDialog.spec.ts` | +2：初始焦点在取消（attachTo 真实挂载）、提交在途遮罩否决 |

### 说明与遗留

- P2-8 选"主区下移"方案（报告给出的两个方向之一）：横幅出现时主区整体下移
  一行，关闭还原；kafka 的 top:70px 遮罩方案在 ldap 表单高度下仍会压住范围
  字段，故未采用。
- Schema/Connections 遮罩 `@click.self` 保持现状（无否决语义，接入等价），
  已在扫描报告收口表标注 optional。
- 树 aria-tree 语义与 twisty 停止位收敛（完整 roving tabindex）仍留后续：
  本轮按报告建议先收口"↑/↓ 移动 + Enter 选中"。

## UI 扫描第 4 轮修复收口（2026-09-06）

第 3 轮专家视角深度测试（P1×1、P2×9）全部修复并复验，改动均限 `ldap/frontend/`：

- **P1-3 导出子树静默截断**：导出上限提至 5000、消费 `truncated`，截断时通知告知数量（七语）。
- **P2-13**：LDIF 模式 dn 行锁定（变更忽略 + 可见提示 + 保存仍发往原 DN）；无属性 diff 时通知「没有需要保存的修改」。
- **P2-14**：同名属性行多值合并去重；**P2-15**：count===sizeLimit 时「已到上限」徽标；**P2-16**：数值输入行内校验（留空/0 = 不限制）。
- **P2-17/18/19**：5 弹窗 role=dialog/aria-modal；树 role=tree、节点 treeitem/aria-level、表头 aria-sort；button 嵌套清零（twisty/徽标降为 span[role=button]）。
- **P2-20/21**：ModifyDnDialog RDN 预检（复用 isLikelyRdn）；预设删除前确认。
- i18n 七语新增/整备：numericHint、invalidNumber、presetRemoveConfirm、ldifDnLocked、noChanges、atLimitBadge（i18n.spec 键集守卫通过）。

验证：`pnpm typecheck` 0 错；`pnpm test` 23 文件 291 用例全绿（基线 279 + 12）；playwright 复验 10/10 断言通过、0 pageerror。修复状态已逐条回填 `UI_SCAN_FINDINGS.zh-CN.md`。未提交 git。

## UI 扫描第 6 轮修复（第 5 轮复核新发现，2026-09-06）

第 5 轮复核扫描新发现 P2×4 全部修复：P2-22 表头 aria-sort 接线（补单测）、P2-23 LDIF dn 锁定提示实时化（watch）、P2-24 连接切换工作台状态重置（结果表/搜索态/写弹窗）、P2-25 删除/改名后结果表联动重搜。验证：typecheck 0 错、295 用例全绿（+1 aria-sort）；浏览器复验 aria-sort/实时提示/删除联动通过，P2-24 留真机多连接复验。

## 工作台滚动条隐藏：条体不再常驻显示（2026-09-09）

`style.css` 全局滚动条由"6px thin 常驻"改为全部隐藏（`scrollbar-width: none` +
`::-webkit-scrollbar { display: none }`），滚动仍由滚轮/触控板/键盘驱动。原先对
webkit 伪元素定制宽高会把滚动条从悬浮态固化为占位常驻态，与宿主观感不符。
改动仅 `ldap/frontend/src/style.css`；验证：`pnpm typecheck` 0 错、`pnpm test`
29 文件 401 用例全绿。

## 插件数据目录 fallback 改为持久化路径（2026-09-09）

根因：宿主拉起 sidecar 时从未注入 `DBX_PLUGIN_DATA_DIR`，插件一直走
`os.TempDir()/dbx-plugin-data/io.dbx.ldap` 兜底；macOS `$TMPDIR` 在重启时清空，
prefs.json / presets.json / audit.jsonl 全部丢失（ssh 插件先发现，ldap 同构）。

修复（`internal/store`）：新增纯函数 `ResolveDataDir(getenv func(string) string,
goos string) string`，按四插件统一顺序解析——① `DBX_PLUGIN_DATA_DIR` 原样；
② `DBX_DATA_DIR` → `<root>/plugin-data/io.dbx.ldap`；③ 平台持久用户数据目录
下 `dbx-plugin-data/io.dbx.ldap`（darwin `$HOME/Library/Application Support/...`、
其余 unix `${XDG_DATA_HOME:-$HOME/.local/share}/...`、windows `%APPDATA%\...`）；
④ 全缺时才回落 `os.TempDir()`，永不失败。`Open()` 传 `os.Getenv` 与
`runtime.GOOS`；不用 `os.UserConfigDir()`（Linux 语义是 config 不是 data）。
附带统一 `internal/ldapconn` 的 `krb5TempDir()`：复用同一解析函数落
`<数据目录>/krb5/`（store 仅依赖标准库，无循环依赖；临时 krb5.conf 连接结束即删）。

验证：`go test ./internal/store/... -run . -v` 12 例全绿（新增 `TestResolveDataDir`
表驱动 8 例：优先级/空白未设/DBX_DATA_DIR/darwin/XDG 两种/windows/TempDir 兜底；
原 TempDir 兜底用例改为断言不再落 TempDir）；`go test ./...` 全绿；
`go vet`、`go build` 干净。本机实测解析到
`/Users/Jinpy/Library/Application Support/dbx-plugin-data/io.dbx.ldap`。
未提交 git。

## DN 树同级排序：OU 先于 CN（2026-09-10）

用户诉求：左侧 DN 树 `ou=` 条目排在 `cn=` 前（字典序下 "cn"<"ou" 会反着排）。

- 改动：`lib/dnTree.ts` 新增纯函数比较器 `compareDnForTree`——主键按首个 RDN
  属性类型分组（ou→0，cn→1，其余类型/异常 DN→2 排最后，大小写不敏感），次键
  保持原全 DN `localeCompare(undefined, {numeric:true})`，组内次序不变；
  `DnTree.vue` `fetchChildren` 的排序替换为该比较器（懒加载/加载更多/invalidate
  全走此路径）。过滤命中列表（compareDnByLabel）与 Go 后端未动。
- 验证：`pnpm typecheck` 0 错；`pnpm test` 29 文件 406 用例全绿（dnTree.spec
  新增 5 例）；`pnpm build` 通过。

## 条目关联视图：对标 ADUC Members / Member Of（2026-09-11，并发 agent 轮）

用户诉求：AD 组（如 `CN=CORP_C_GG_GITHUB_User,...`）在树里展开永远为空、
组成员/所属关系无处可看，确认为 PLA_GAP_ANALYSIS 有意放弃项，本轮补齐。
"对标 active role" 按 ADUC（Active Directory 用户和计算机）的
Members / Member Of 双向语义实现。设计定稿与任务条目见 IMPL_PLAN §5.4
（L5-1～L5-4）：**无新增协议方法、无后端改动**——Members 直读条目自身
`member`（回退 `uniqueMember`）属性；Member Of 前端组合既有 `ldap/search`
（连接 Base DN / scope=sub / `(member=<本条目DN>)` / attributes=["1.1"] /
sizeLimit=1000），过滤器不受屏蔽属性策略影响（`memberOf` 在默认屏蔽表也不
碍事），且不依赖 memberof overlay。

### 改动清单

| 文件 | 改动 |
|---|---|
| `frontend/src/components/AssociationPanel.vue` | 新增：Members/Member Of 双 section 只读面板——member/uniqueMember 大小写不敏感直读、VirtualList 虚拟化（rowHeight 26）、行点击 emit `openEntry`、复制 DN；Member Of 惰性搜索（`active` 门控）含 loading/空态/截断/失败重试，请求序号防竞态 |
| `frontend/src/components/AssociationPanel.spec.ts` | 新增 10 例：member 渲染/点击、空态、uniqueMember 回退、搜索参数与转义（RFC 4515）、active 门控、失败重试、截断、dn 变化重缓存 |
| `frontend/src/components/EntryEditorDialog.vue` | `ldifMode` 布尔重构为 `editorTab: form/ldif/assoc` 三态（form↔ldif 行为等价迁移）；mode-switch 第三页签（仅 view 态）；assoc 态挂 AssociationPanel 并透传 openEntry/error/notify；新 props `baseDn`/`initialTab`；关联页签隐藏编辑/保存动作（只读视图） |
| `frontend/src/components/EntryEditorDialog.spec.ts` | +4 例：view/add 页签可见性、panel props 断言、openEntry 冒泡、initialTab 直开关联页（对 panel 用 vi.mock 隔离） |
| `frontend/src/components/DnTree.vue` | 右键菜单 view 后新增「成员」项 → emit `members` |
| `frontend/src/App.vue` | `openEntry` 增可选 `initialTab`（默认 form，既有调用方不变）；`openEntryAsMembers` 以关联页直开；DnTree `@members` 接线；编辑器传 `base-dn`/`initial-tab`、监听 `@open-entry`（关联内点条目复用换内容） |
| `frontend/src/lib/i18n.ts` | 七语 +12 key：`editor.assocMode` + `associations.*`（members/memberOf/count/emptyMembers/emptyMemberOf/loading/truncated/loadFailed/retry/copyDn） |
| `frontend/src/mockDbxHost.ts` | 种子新增 `cn=team-a,ou=groups`（groupOfNames，member→3 个既有用户） |
| `scripts/smoke_test.py` | 新增 S15「member/memberOf association round-trip」：自建临时 groupOfNames → `(member=)` 反查断言 → `(memberOf=)` 探测（overlay 未启用记 PASS 说明）→ 删除后复查 → finally 清理；配套 `escape_filter_value`/`search_dns`/`ensure_groups_ou` |
| `docs/IMPL_PLAN_DBX_LDAP.zh-CN.md` | §5.4 特性章节 + 任务 L5-1～L5-4（含已知限制：AD 主组不解析、嵌套组 1.2.840.113556.1.4.1941 不支持、反查限于 Base DN 子树） |

### 验证

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` | 0 错 |
| `pnpm test` | **30 文件 420 用例全绿**（406 → 420：AssociationPanel 10 + EntryEditorDialog 4） |
| `pnpm build` + `scripts/test.sh` 全套 | 过；UI walkthrough（headless Chrome）**7/7** |
| backend `go vet` / `go test`（四包） | 全绿（本轮后端零改动） |
| smoke（真实 OpenLDAP 容器） | **16/16 PASS**，含 S15（member 反查往返断言全过；memberOf 探测因容器未启用 overlay 记 PASS 说明，组件按 SKIP 语义设计），残留 0 条 |
| smoke（无容器） | test.sh 内 16 场景按设计全 SKIP |

### 说明与遗留

- 关联内点击成员/所属条目 → `openEntry(dn)` 换内容并回落表单页签（继续浏览
  需再点「关联」；如需"关联跳转保持关联页"可后续加参数透传）。
- `dbx-plugin package` 出包当前失败：go.work 列 1.22 而后端 module 要求
  ≥1.24（`go vet/test` 直跑不受影响）——既有环境问题，与本轮无关（后端零
  改动可证），待单独修 go.work。
- 真实 AD（KN-LDAP）上的实际效果未真机复验（需有效凭据，沿袭 §10.3 阻塞）；
  AD 对 `(member=DN)` 等值过滤为原生能力，风险低。
- 未提交 git。

## 通用 DN 引用泛化：managedBy 等 DN 值属性可点击 + 反查（2026-09-11 第二轮，并发 agent 轮）

上轮关联视图只覆盖 member/memberOf；本轮按用户诉求泛化到**所有 DN 值属性**
（managedBy/owner/secretary/seeAlso 等，如 OU 上 managedBy→某用户）：正查
可点击打开目标条目，反查可看"谁引用了我"。设计定稿与任务见 IMPL_PLAN
§5.4.1（L5-5～L5-7）。仍**无新增协议方法、无后端改动**。

### 设计要点

- **DN 属性识别 schema 驱动**：解析 `ldap/schema` attributeTypes，SYNTAX=
  1.3.6.1.4.1.1466.115.121.1.12（DN）/ 1.3.6.1.4.1.1466.115.121.1.34
  （nameAndOptionalUID）或定义含 `SUP distinguishedName`/`nameAndOptionalUID`
  （单层启发，`\b` 边界防误判 matching rule 名）；schema 不可用回退内置
  `DN_REFERENCE_CORE` 七项（managedBy/owner/secretary/assistant/manager/
  seeAlso/altRecipient）。
- **反查限定核心表**：schema 全量 DN 属性可能数百个，不做全量 OR；被引用区
  固定用核心表生成单次 `(|(managedBy=DN)(owner=DN)...)` 过滤器（RFC 4515
  转义、单属性退化、scope=sub、attributes=["1.1"]、sizeLimit=1000）。
- schema 经 App 惰性加载（编辑器首开触发，独立 `dnSchemaCache`，失败静默
  降级兜底表；连接切换时重置），App→EntryEditorDialog→panel 贯通
  `dnAttributes` prop。

### 改动清单

| 文件 | 改动 |
|---|---|
| `frontend/src/lib/dnAttributes.ts` | 新增：`deriveDnValuedAttributes`（schema 解析）/`buildReferencedByFilter`（OR 过滤器）/`DN_REFERENCE_CORE` 等纯函数 |
| `frontend/src/lib/dnAttributes.spec.ts` | 新增 14 例（SYNTAX/SUP/NAME 双形态/去重/转义/退化/空表） |
| `frontend/src/components/AssociationPanel.vue` | 新增 prop `dnAttributes`；「DN 引用」区（按属性分组、行渲染与成员区同款、member/uniqueMember 不重复）；「被引用」区（active 惰性 + 请求序号防竞态，OR 过滤器反查）；`loadFailed` 语义随共用泛化 |
| `frontend/src/components/AssociationPanel.spec.ts` | 10→21 例（双搜索按 filter 分派断言、引用区分组/兜底、被引用 OR/转义/失败重试/截断/空态/dn 重搜） |
| `frontend/src/App.vue` | `dnSchemaCache` + `ensureDnAttributes()`（openEntry 惰性触发，失败静默降级）+ 连接切换重置；`:dn-attributes` 下传 |
| `frontend/src/components/EntryEditorDialog.vue` | 新 prop `dnAttributes` 透传 panel |
| `frontend/src/lib/i18n.ts` | 七语 +4 key：`references`/`emptyReferences`/`referencedBy`/`emptyReferencedBy`；`loadFailed` 七语文案泛化为「关联搜索失败」 |
| `frontend/src/mockDbxHost.ts` | schema 加 managedBy 定义（AD OID 1.2.840.113556.1.4.218）；`ou=services` 种子加 managedBy→uid=user0000 |
| `scripts/smoke_test.py` | 新增 S16「generic DN reference (managedBy) round-trip」：临时 OU 带 managedBy→反查→清理；服务器 schema 未定义 managedBy 时软跳过（PASS 说明，沿袭 S15 memberOf 先例与规则 5） |
| `docs/IMPL_PLAN_DBX_LDAP.zh-CN.md` | §5.4.1 特性章节 + 任务 L5-5～L5-7 |

### 验证

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` | 0 错 |
| `pnpm test` | **31 文件 446 用例全绿**（420 → 446：dnAttributes 14 + AssociationPanel +11 + EntryEditorDialog +1） |
| smoke（真实容器，恢复上轮随机密码直通方式） | **17/17 PASS**；S16 因 bitnami 容器 schema 未定义 managedBy 软跳过（断言路径：临时 OU + managedBy 写入 → `(managedBy=)` 反查，单测/mock 侧全覆盖；AD 原生定义 managedBy，真实域不触发该跳过）；S15 memberOf overlay 说明沿袭 |
| 容器状态 | 验后 `docker stop` 恢复原 Exited 态；密码全程未打印/未落库 |

### 说明与遗留

- 反查覆盖面限定核心表（schema 中其余 DN 属性只正查可见）；如需按 schema
  全量反查需分批 OR/服务端索引评估，暂无诉求。
- `dnSchemaCache` 复用 `useLdapSchemaCache` 的 attributeNames 槽存原始
  attributeTypes（该 hook 缓存不保留原文，另起缓存的最小实现），后续如
  schema 消费方增多可考虑 hook 层暴露 raw 槽位。
- 出包 go.work 问题、KN-LDAP 真机凭据阻塞沿袭上轮；未提交 git。

## 关联视图 UX 复审：子页签去拥挤 + 树「成员」右侧搜索联动（2026-09-11 第三轮，并发 agent 轮）

用户反馈：关联页签四区纵向堆叠太拥挤、列表无搜索、不像表格；提议树右键
「成员」直接组织过滤器在右侧搜索。两条都落地（设计定稿补记 IMPL_PLAN
§5.4.1「交互形态」段）：

### 改动清单

| 文件 | 改动 |
|---|---|
| `frontend/src/components/AssociationPanel.vue` | 重构：四 section 堆叠 → 「成员/所属/DN 引用/被引用」**互斥子页签**（`.mode-switch` 样式 + `.tree-badge` 计数徽标，搜索类加载完才出数字）；顶部常驻**本地过滤框**（激活列表 DN 大小写不敏感子串过滤，dn 变化清空，过滤无匹配显示 `associations.noMatch`）；行统一两行式固定行高 44（首行 RDN+来源属性小字、次行完整 DN ellipsis），VirtualList resetKey 纳入 dn/页签/过滤词；DN 引用区取消分组小标题改行内属性小字（等高行约束）。搜索/防竞态/重试/截断逻辑原样保留，props/emits 契约未动 |
| `frontend/src/components/AssociationPanel.spec.ts` | 21→25 例：子页签渲染/互斥/徽标计数（含受控 promise 未完成不出数字）、本地过滤（收敛/大小写/noMatch/清空/作用反查列表/dn 清空），既有用例全部迁移页签内断言 |
| `frontend/src/lib/i18n.ts` | 七语 +2 key：`filterPlaceholder`、`noMatch` |
| `frontend/src/components/SearchForm.vue` | 新暴露 `runFilterAt(baseDn, filter)`：源码模式承载调用方过滤器 + applyBaseDn + scope=sub + 立即 emit run；空过滤器防呆回退 (objectClass=*)；disabled 直接 return |
| `frontend/src/components/SearchForm.spec.ts` | +3 例：runFilterAt 过滤器/Base/scope/payload 断言、disabled 不 emit、空白过滤器回退 |
| `frontend/src/App.vue` | `@members` 改接 `searchMembersAt(dn)` = `runFilterAt(连接Base, (memberOf=<DN>))`（escapeLdapFilterValue 转义）；清理死接线（editorInitialTab/openEntryAsMembers/:initial-tab；EntryEditorDialog 组件 initialTab prop 保留） |

### 验证

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` / `pnpm test` | 0 错 / **31 文件 453 用例全绿**（446→453：AssociationPanel +4、SearchForm +3） |
| 浏览器实机（vite 5299 + playwright-core + 系统 Chrome headless，mock.html，1440×900；一次性脚本不入库，截图 `/tmp/dbx-assoc-ui/`） | ① team-a 关联页签：过滤框 + 四子页签徽标（成员 3）+ 两行式行渲染；② 本地过滤输 user0001 仅剩该行、清空恢复；③ ou=services 的 DN 引用页签显示 managedBy→user0000；④ 树右键 team-a → 成员：右侧立即以源码模式 `(memberOf=cn=team-a,ou=groups,dc=demo,dc=dbx)`、Base=连接根、scope=sub 自动执行（mock 用户无 memberOf 属性故 0 条，AD 原生支持） |

### 说明与遗留

- 分页诉求以 VirtualList 虚拟滚动 + 计数徽标承载（千级行直接滚动，无需翻页）；
  右侧结果表路径（树「成员」）自带分页/排序/导出，两条路径互补。
- 服务器无 memberof overlay 时树「成员」搜索为空——关联页签成员区
  （member 属性直读）是全服务器通用路径，文案已按此语义设计。
- 未提交 git。

## review 第 1 轮：编辑器 LDIF 数据丢失修复 + 关联页签保持 + 两处 P2（2026-09-11，goal-state round1）

review + 持续优化第 1 轮（状态与报告存
`.goal-state/report-ldap-round1.md`）。改动全部限 `ldap/frontend/`，
后端零改动（service/operations 只读 review 未发现需修项）。无新 i18n key。

### 发现并修复（4 项）

| 级别 | 问题 | 修复 |
|---|---|---|
| P1 | 编辑器 LDIF 页签编辑 → 直接切「关联」（不经表单）→ 切回表单：LDIF 编辑不落 rows 且被 rows→LDIF 重同步**静默覆盖**（数据丢失） | `EntryEditorDialog.vue` 新增 `leaveLdif()` 共用守卫（解析失败保持 LDIF 页签），switchToForm/switchToAssoc 走同一路径；+2 单测（assoc 路径保留编辑、解析错误阻止切走） |
| P2 | 连接切换不清空「最近操作」审计面板（旧连接 denied/ok 与新连接反馈混排） | `App.vue` syncConnectionContext 切换分支清空 `auditItems` |
| P2 | 关联视图内点击条目跳转回落表单页签打断浏览（§14 记录的遗留候选） | `App.vue` openEntry 增可选 `initialTab` + `editorInitialTab` ref，`@open-entry` 传 `'assoc'`；常规入口行为不变 |
| P2 | 新结果不含旧排序列时排序指示残留（该列全空=没排，▲/▼ 却还在） | `ResultTable.vue` watch(entries) 重置失效排序键回 dn 升序；+2 单测（消失重置/仍存在保持） |

记录未修（低影响）：`exportResults` catch 不走 showError 统一通道；mock 桥
entry/delete 不支持 recursive；AssociationPanel inline style 可维护性；
pagedSearchEntries 恰好等于上限也置 truncated（前端 atLimit 徽章已对冲）。

### 验证

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` / `pnpm test` | 0 错 / **31 文件 457 用例全绿**（453 + 4 新增；首跑 1 例失败为本用例夹具错误，修正后过，非产品代码问题） |
| `pnpm build` | 过（ui/index.html） |
| `go vet` / `go test -count=1 ./...` | 全绿（后端零改动回归确认） |
| `scripts/test.sh` 全套 | 前端三件套 + go 过；smoke 无容器 17 SKIP（设计内）；打包失败为既有 go.work（1.22 vs module ≥1.24）问题，沿袭 §14 遗留，与本轮无关 |
| `scripts/smoke_container.py`（真实容器，随机密码仅经环境变量） | **17/17 PASS**（S15/S16 沿袭 PASS 说明），down -v 清理 |

浏览器复核留人工：① LDIF→关联→表单编辑保留；② 关联内跳转保持关联页签；
③ 多连接切换清空审计面板（mock 单连接无法复现）。未提交 git。

## review 第 2 轮：打包链路修复 + 遗留项清零（2026-09-11，goal-state round2）

review + 持续优化第 2 轮（报告存 `.goal-state/report-ldap-round2.md`）。主线
为第 1 轮遗留项；Go 后端零改动（仅回归）。

### 关键澄清：打包失败根因修正（round1 归因不完整）

工作区内并无 go.work 文件。真实链路：npm wrapper `bin/dbx-plugin.js` 在
`DBX_PLUGIN_SDK_ROOT` 未设置时**自动注入 bundled sdk-root**，CLI 带该变量
以 workspace 方式构建 Go 后端且 workspace 钉 go 1.22，与 backend go.mod
`go 1.24.0` 冲突。round1 `scripts/test.sh` 打包分支走 wrapper 故失败；
`build.sh` 直调 native CLI 早已绕开。本轮把 test.sh 打包分支改为与
build.sh 同款直调，出包恢复（dist/*.dbxp，已 ignore 不入库）。

### 改动（5 文件 + 2 spec）

| 项 | 内容 |
|---|---|
| P1 打包 | `scripts/test.sh` 打包分支 `env -u DBX_PLUGIN_SDK_ROOT` 直调 native CLI（同 build.sh），全套 test.sh 实测出包成功 |
| P2 遗留3 | `App.vue` exportResults catch 改 `showError(cause)`；`result.exportFailed` 七语 i18n 同删（全 src 无引用） |
| P2 遗留4 | `mockDbxHost.ts` entry/delete 支持 recursive（fixture 简化版：子树全删 + 聚合审计 subtree_delete/deletedCount，镜像后端形状）；+4 单测 |
| 打磨① | `ResultTable.vue` 单元格 `cellTitle()`（全量值，2000 封顶）+ `:title`；+2 单测 |
| 打磨②③ | 评估不做：DnTree 命中 makeNode"丢展开"前提不成立（selectNode 只消费 dn）；AssociationPanel 40 处 inline style 超小修边界，沿袭 |

### 验证

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` / `pnpm test` | 0 错 / **32 文件 463 用例全绿**（457 + mock 4 + tooltip 2；首跑 1 例失败为本用例长度断言过严，放宽后过） |
| `go vet` / `go test` | 四包全绿（零改动回归） |
| `scripts/test.sh` 全套 | 全绿含**打包成功**；UI walkthrough 7/7；smoke 无容器 17 SKIP（设计内） |
| `scripts/smoke_container.py`（随机密码仅经环境变量） | **17/17 PASS**（S15/S16 沿袭 PASS 说明），down -v 清理 |

沿袭/下一轮：宿主 e2e 已具备条件（.dbxp 出包恢复）；KDC/NTLM 真机沿袭；
build.sh/test.sh native CLI 路径写死 darwin-arm64 的平台解析为下轮候选；
browser 复核三点沿袭 + mock 子树删除走查。未提交 git。

## review 第 3 轮：收敛评估（2026-09-11，goal-state round3）

review + 持续优化第 3 轮（报告存 `.goal-state/report-ldap-round3.md`）：主线
为 round2 遗留 1（宿主 e2e）与遗留 5（平台解析），另做四个改动面针对性复核。
前端产品代码与 Go 后端**零改动**（纯复核）。

### 改动（2 改 + 1 新增，均 scripts/）

| 项 | 内容 |
|---|---|
| 遗留5 | 新增 `scripts/cli-platform.sh`：`cli_platform()` 纯映射（Linux 包带 `-gnu` 后缀：`linux-x64-gnu`/`linux-arm64-gnu`）+ `resolve_native_plugin_cli()`；`build.sh`/`test.sh` 接入，解析失败 stderr 明确 WARN 后回退 wrapper（darwin-arm64 行为不变，向后兼容）；9 项干跑断言全过 |
| e2e | `shared/host-e2e/install.sh ldap` 装 0.1.56 进隔离 app-data（激活记录 sequence 3/previousVersion 0.1.24/camelCase 校验过）；双冒烟以安装副本 sidecar 对容器 **17/17 PASS**（密码仅经 `LDAP_ADMIN_PASSWORD` 环境变量），容器 down -v 清理 |
| 复核 | 四个改动面（EntryEditorDialog 页签守卫、App.vue 审计/页签接线、ResultTable 排序与 title、mockDbxHost delete）走查**无 P1/P2 新发现**；两条非缺陷观察（App.vue busy 死状态、mock 非 recursive 删非叶不模拟 LDAP 66），仅记录不修 |

### 验证

| 套件 | 结果 |
|---|---|
| `pnpm typecheck` / `pnpm test` | 0 错 / 32 文件 463 用例全绿（基线保持） |
| `go vet` / `go test -count=1` | 四包全绿（强制非缓存） |
| `scripts/test.sh` 全套 | all green：UI walkthrough 7/7、打包走新解析路径出包成功（无 fallback WARN）、smoke 无容器 17 SKIP（设计内） |

### 收敛判定

e2e GUI 拉起为卡点（宿主 debug bundle 未构建，构建 + UI 验收超 subagent
可脚本化范围，如实记录未绕过）。**无剩余可执行项（仅剩人工/真机复核项与
维持项）**：GUI e2e 验收（宿主 debug bundle 构建后人工走查插件中心/工作台/
连接浏览）、browser 复核三点 + mock 子树删除走查、KDC/NTLM 真机沿袭；
维持项：AssociationPanel inline style（专项分批）、truncated 恰等上限语义
（前端 atLimit 徽章已对冲）、plugin-cli 上游建议（仅记录）。未提交 git。


## review 第 4 轮：fresh review 四组小修 + 新 P2 清单（2026-09-12，goal-state round4）

按空/加载/错误态、表单校验、a11y、大目录性能、mock/真实桥五个未扫面复核；
完整报告：`.goal-state/report-ldap-round4.md`，任务清单：IMPL_PLAN R4-1～R4-5。
本轮四组改动全部位于 ldap/，后端产品代码、宿主与公共层未改，无新依赖：

- **P1 宿主事件接线**：优先当前 SDK 的 `onContext`，旧 `onContextChange`
  降级；处理 `onEvent` 的 env.locale，类型与 mock 同步。实际宿主 SDK 生成的
  iframe 验证通过（新 connectionId/baseDn 与语言更新）。
- **P2 搜索校验**：完整整数/安全整数检查，搜索、树快捷入口、预设保存共用
  门禁；源码/数值错误与输入框关联，修正即时恢复。
- **P2 目录树**：过滤失败原地重试、刷新当前视图、等待期旧请求作废与卸载
  取消；补 loading/error/empty 播报、懒节点/过滤项 aria、左右键展开折叠。
  `tree.retry` 七语齐全。
- **P2 mock 计数**：补 `ldap/entry/childrenCount` 的 dn 输入、count/truncated
  返回及空 DN 拒绝；原 recursive delete 夹具不变。

验证：前端 **34 文件 / 487 用例全绿**（新增 24）+ typecheck/build；后端
`go vet` / `go test -count=1 ./...` 通过；浏览器 **11/11**；真实容器
**17/17 PASS**（S15 memberOf / S16 managedBy 的条件跳过沿袭，已 down -v）。
全套脚本首次因验证 shell 选中 /usr/local npm 导致 native CLI 探测失败；
显式前置既有 nvm PATH 后重跑通过，出包 `io.dbx.ldap-0.1.57-darwin-arm64.dbxp`。
未修改前轮打包脚本，起始 manifest 内容哈希保持一致。

大目录仅评估：10000 条 fixture 下树 **39 DOM 行**、结果 **50 行 / 200 页**；
发现 count 上限 5000 被当成总数后续载入口消失，留独立性能任务。
**未达成无剩余可执行项**：编辑器 objectClass/MUST 与 DN 预检、搜索/详情
状态、presets save/remove 真实回包与 mock 偏差、剩余树键盘/夹具保真度待
下一轮。round1–3 已修项未重复修改，指定维持项不动；未提交 git。


## review 第 5 轮：round4 P2 四组跟进（2026-09-12，goal-state round5）

完整报告：`.goal-state/report-ldap-round5.md`；任务清单 IMPL_PLAN R5-1～R5-4。
本轮继续叠加 LDAP 内小修，后端产品代码/宿主/公共层未改，无新依赖：

- **R4-08 预设契约**：save/remove 对齐真实单项/成功回包，按服务端 ID 更新；
  过滤器串恢复构建器，重复提交门禁，mock 镜像字段及错误。
- **R4-05 必填反馈**：objectClass → MUST/SUP 即时提示，缺失项可点击定位；
  保留原不可读必填属性与隐藏密码容错，不要求普通编辑补写被屏蔽属性。
- **R4-06 DN 预检**：空 AVA、转义/引号检查，LDIF 解析后完整 DN 再校验；
  合法转义和新增态父 DN 往返保留，前轮离页守卫未改。
- **R4-07 请求状态**：搜索/详情 loading/error/empty 与重试，旧读取结果失效，
  关闭或切换连接后不再被旧响应重新打开；六个新 key 七语齐全。

验证：前端 typecheck/test/build **36 文件 / 528 用例**（+41），Go vet 与
`go test -count=1 ./...` 四包通过；UI **16/16**（+5）；真实 sidecar 预设
保存/更新/重启读取/删除四组验证通过；容器 **17/17 PASS**（S15/S16 条件
跳过沿袭，已 down -v）。`scripts/test.sh` 全套及 0.1.57 打包通过。
起始 manifest 哈希未变。新增测试曾有 confirm 桩、DN 转义预期和 UI Base DN
场景设置错误，已修正；原始失败及最终日志入口见报告。

**未达成无剩余可执行项**：R4-10 树剩余键盘操作、R4-11 mock 保真度待后续；
另确认现有 smoke scenario 成功时调用场景两次（R5-05，纯计数器复现，仅记录）。
大目录保持仅评估，指定维持项与人工/真机验证继续保留；未提交 git。

## review 第 6 轮：树键盘、mock 契约与 smoke 收口（2026-09-12，goal-state round6）

完整报告：`.goal-state/report-ldap-round6.md`；任务清单 IMPL_PLAN R6-1～R6-4。
本轮四组小修，无 Go 产品代码/宿主/公共层改动，无新依赖，复用既有七语：

- **R4-10**：父子/Home/End 与跨虚拟窗口导航，原生加载更多按钮可 Tab/Enter
  操作；续载期间保留节点焦点，分页和虚拟化策略不变。
- **R4-11 读取**：mock 属性选择、typesOnly、成功分页聚合、未知方法失败；
  真实 RootDSE 默认只给 objectClass，前端导出改为显式请求 `["*", "+"]`。
- **R4-11 写入**：按值增删、失败原子性、RDN 属性与子树迁移。另修复 P1：
  前端的 newParentDn 被 Go 忽略，现发送真实字段 newSuperior；容器 S5 验证
  子树移动与 deleteOldRdn 两态。
- **R5-05**：smoke 场景只执行一次并保留 note；新增标准库离线测试入全套。

验证：前端 typecheck/test/build **37 文件 / 554 用例**（+26），UI **20/20**，
Python **3 个测试方法**（含 6 个异常/SKIP 子例）；独立 Go vet / count=1 四包
通过；容器两次 **17/17 PASS**（S15/S16 部分断言条件跳过沿袭，已 down -v）。
`scripts/test.sh` 全套与 0.1.58 打包通过。manifest 在执行期间由本轮之外的
操作从 0.1.57 变为 0.1.58，本轮保留该变化，没有修改或还原此文件。

如实记录：首次树新增测试 3 例失败为假时钟事件派发/测试 DOM 默认属性差异，
修正测试后浏览器实按键通过；额外分页协议探测曾因 LDAP Code 4 失败。7 条
目录下 pageSize/sizeLimit 为 2/3、3/3、10/3 会报 Size Limit Exceeded，1/3、
2/4 可返回；该后端/服务器控制交互未修，原始日志在报告中。

**未达成无剩余可执行项**：高级过滤器/匹配规则、别名与服务器控制错误的
mock 保真度仍待分批推进；分页问题单列后端专项。大目录仅评估、truncated、
AssociationPanel inline style、真机认证、GUI e2e 与 plugin-cli 建议继续维持。
未执行 commit/push/PR/stash/reset/checkout。
