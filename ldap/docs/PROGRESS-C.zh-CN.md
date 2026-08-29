# L-C 路进度记录（Vue 前端 / 脚本 / 打包验证）

> 记录人：L-C（dbx-ldap-plugin 前端、scripts、打包验证）。
> 日期：2026-08-28。工具链：node 22.21.0 / pnpm 10.27.0 / dbx-plugin CLI 0.1.0 / go 1.27.0（经 /tmp/go-shim 剥 GOWORK）。

## 1. 完成项

### 1.1 frontend 工程（frontend/）

- 脚手架照 ssh-sftp/frontend：`package.json`（vue 3.5 + vite 8 + typescript 6 + vitest 4 + vue-tsc，**零新增依赖**）、`vite.config.ts`、`tsconfig.json`（strict、Bundler resolution）、`index.html`、`src/main.ts`、`src/env.d.ts`（Host API 1.0 类型面）、`src/style.css`。
- `build.mjs`：vite 构建 → 产出**自包含单文件** `ui/index.html`（JS/CSS 内联、校验 script 结构），即 manifest `ui.root` 的产物。

### 1.2 lib 工具层（frontend/src/lib/）

| 文件 | 来源/职责 |
|---|---|
| `ldapFilter.ts` | tiny-rdm utils/ldapFilter.js 搬运：RFC 4515 校验/转义 |
| `dn.ts` | dn.js：DN 解析/拆分/RDN 构造 |
| `ldif.ts` | ldif.js：LDIF 序列化/解析（改 TS，双向） |
| `ldapExporter.ts` | ldapExporter.js：LDIF/CSV/JSON 导出 |
| `baseDn.ts` | baseDn.js：base DN 层级拆解（树根） |
| `schemaCache.ts` | useLdapSchemaCache 语义：进程内缓存 + `ldap/schema` refresh |
| `api.ts` | sidecar 封装：`connectionId` 注入、方法名照实施文档 §5.2（`ldap/search`、`ldap/entry/{get,add,modify,delete,modifyDn}`、`ldap/rootDse`、`ldap/schema`、`ldap/connections/statuses`、`ldap/presets/{list,save,remove}`），camelCase 参数 |
| `i18n.ts` | **七语全量** zh-CN/zh-TW/en/es/it/ja/pt-BR，key 树（tree/search/result/editor/deleteDialog/modifyDn/schema/connections/rootDse），`t(key, values)` + `resolveWorkbenchLocale` + 响应式 locale |
| `ldapDiff.ts` / `dnTree.ts` / `appearance.ts` | 条目 diff、树辅助、DBX 外观令牌解析 |
| `*.spec.ts` × 5 | vitest 单测 68 例：filter 合法/非法与转义、LDIF 往返、DN 解析、exporter、i18n key 集合七语一致性 |

### 1.3 组件（frontend/src/components/ + App.vue）

- `App.vue`：工作台外壳；连接上下文经 `window.dbxPlugin`（`ready`/`context`/`onContextChange`/appearance/locale 订阅），connectionId 注入 api 层，`connection.external_config.base_dn` 兜底取 base DN；`showError(cause, "ldap")`；`ldap/audit` 事件轻提示；导出走 Blob URL 兜底。
- `DnTree.vue`（+ `TreeBranch.vue` 递归）：scope=one 懒展开、关键字远程过滤（buildTreeKeywordFilter 语义）、右键菜单（查看/新增/重命名/删除/导出子树/在此搜索）。
- `SearchForm.vue`：filter（前端先过 validateLDAPFilter）/scope/attributes/sizeLimit/pageSize/typesOnly/derefAliases + 预设 CRUD。
- `ResultTable.vue`：列 = attributes 并集，前端分页排序，导出 LDIF/CSV/JSON。
- `EntryEditorDialog.vue`：查看/编辑/新增，属性行编辑 + LDIF 双向（parse 错误回显）。
- `DeleteEntryDialog.vue` / `ModifyDnDialog.vue` / `SchemaPanel.vue`（attributeTypes/objectClasses 浏览 + 强制刷新）/ `ConnectionsPanel.vue`（`ldap/connections/statuses`）。
- 样式沿用 ssh-sftp 的 CSS 变量令牌体系（--background/--foreground/--muted/--accent/--border/--destructive + dark scheme）。

### 1.4 scripts/

- `sidecar_client_jsonl.py`：M0 §5.1 规格（NDJSON 行协议），接口照 framed 版：`start/start_default`（未导出 start_default，用 `SidecarClient.start()`，见下）、`initialize`、`request`（含 on_event 子请求钩子）、`wait_event`、`notify`、`close`；`lifecycle_params(connection)`（provider/connection/runtime，M0 §3.1）；`is_method_not_registered`（-32601/not implemented → SKIP 语义）。
- `smoke_test.py`：实施文档 §8 的 S1-S10 全场景，装饰器计 PASS/FAIL/SKIP；未实现方法 SKIP、无 OpenLDAP 容器整段 SKIP（`LDAP_TEST_REQUIRE=1` 翻成 FAIL 供 CI）。
- `build.sh`：前端 install+typecheck+test+build → `dbx-plugin package .`（backend go build 步骤独立容错）。
- `test.sh`：前端三件套 → go vet/test（backend 就绪时）→ 打包 → smoke，全程 SKIP 语义不误报。

## 2. 验证记录（2026-08-28 21:20-21:22）

| 验证 | 结果 |
|---|---|
| `pnpm --dir frontend typecheck`（vue-tsc --noEmit） | PASS |
| `pnpm --dir frontend test` | **68/68 通过**（5 个 spec 文件） |
| `pnpm --dir frontend build` | PASS → `ui/index.html`（168 KB 自包含） |
| backend `go build`（CGO_ENABLED=0） | PASS（bin/dbx-plugin-ldap，5.9 MB；L-B 已完成实现，ldapconn/lifecycle/store go test 全 ok） |
| `dbx-plugin package .`（/tmp/go-shim 剥 GOWORK） | PASS → `dist/io.dbx.ldap-0.1.0-darwin-arm64.dbxp`（5.1 MB：manifest + bin/darwin-arm64 + ui/index.html + assets + checksums，unsigned review candidate） |
| `scripts/test.sh` 全程 | 全绿（smoke S1-S10 整段 SKIP：本机 389 无 OpenLDAP 容器） |
| smoke SKIP 语义 | 已验证：无容器时 10 项全 SKIP、退出码 0 |

## 3. smoke 场景覆盖表（代码就绪，待容器跑通）

| # | 场景 | smoke_test.py | 状态 |
|---|---|---|---|
| S1 | initialize + connection/test (simple) | `run_s1` | 代码就绪，容器不可用 SKIP |
| S2 | connect + base scope root 搜索 | `run_s2` | 同上 |
| S3 | sub + pageSize 分页聚合 = 全量 | `run_s3` | 同上 |
| S4 | add/modify/get/delete 往返读回一致 | `run_s4` | 同上 |
| S5 | modifyDn 改 RDN，新可查旧不存在 | `run_s5` | 同上 |
| S6 | read_only 写拒绝（-32000） | `run_s6` | 同上 |
| S7 | userPassword 屏蔽属性不泄露 | `run_s7` | 同上 |
| S8 | 白名单外 DN 读拒绝 | `run_s8` | 同上 |
| S9 | disconnect 后再调报连接不存在 | `run_s9` | 同上 |
| S10 | 非法 filter 报错文案含 filter | `run_s10` | 同上 |

容器跑法：起 OpenLDAP（bitnami/openldap，`dc=example,dc=org`，admin/adminpassword 或以 `LDAP_TEST_*` 环境变量覆盖）后
`DBX_PLUGIN_SIDECAR=backend/bin/dbx-plugin-ldap python3 scripts/smoke_test.py`。

## 4. 本路修复/决策

- `scripts/build.sh` 的 backend go build 原在仓库根执行 `./backend`（go.mod 不在该层）→ 已改为 `cd backend && go build -o bin/dbx-plugin-ldap .`，产物路径与 `sidecar_client_jsonl.default_binary()` 的 fallback（`backend/bin/dbx-plugin-ldap`）对齐。
- 打包必须经 `/tmp/go-shim`（剥 CLI 硬编码 GOWORK 的 go.work go 1.22，见 M0_FINDINGS §2.1）；build.sh 未内置 shim（机器本地产物，不宜写死 /tmp 路径），打包命令：
  `PATH="/tmp/go-shim:$PATH" bash scripts/build.sh`。
- `SidecarClient.start_default` 实际类方法名为 `start`（framed 版同名语义）；M0 §5.1 的 `start_default` 以 `SidecarClient.start()` 兑现，别名未加（避免无用代码），如 smoke 外部脚本有依赖可再加一行别名。

## 5. 遗留与交接

1. **容器 smoke 未实跑**：本机无 OpenLDAP 容器，S1-S10 全 SKIP。建议 M1 收尾时起容器实跑一轮并回填本文 §3 状态列（后端 L-B 路的领域方法已实现，预计可直接转 PASS）。
2. **宿主端到端**：.dbxp 已产出（unsigned review candidate），尚未安装到测试宿主 `~/btroot/dbx-plugin-host-worktree/.../DBX.app` 验证连接表单（url 绑定 host 形态，M0-T6②）与工作台打开（M0_FINDINGS §2.5 的 URL 解析决策待宿主侧确认）。
3. **浏览器截图验证**（L1-8 DoD）：工作台 UI 已构建通过，但需要宿主或 mock 宿主桥环境才能渲染真实数据；如需截图留档，可临时以 `window.dbxPlugin` mock 页驱动。
4. **i18n key 新增流程**：后续加文案需七语同步 + `i18n.spec.ts` 的 key 集合一致性测试兜底（现 68 例含 5 例 i18n 校验）。
5. 后端仍属并行路：`backend/**`、`manifest.json`、`go.mod` 本路未改动（仅只读验证其编译）。

## 6. 阻塞

无硬阻塞。前端三件套与打包均绿；唯一待外部条件的是 OpenLDAP 容器（smoke 实跑）与测试宿主（安装验证），均非本路可解决，已列为交接项。
