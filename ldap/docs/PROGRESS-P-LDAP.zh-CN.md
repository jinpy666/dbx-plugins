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
