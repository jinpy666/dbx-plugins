# A-LDAP 路进度记录（dbx-ldap-plugin UI 专业化增强：对标 tiny-rdm LDAP Console）

> 记录人：A-LDAP（dbx-ldap-plugin UI 专业化增强）。
> 日期：2026-08-29。工具链：go 1.27.0（darwin/arm64）/ node 22.21.0 / pnpm 10.27 / headless Chrome（playwright-core + 系统 Chrome，仅 /tmp 临时目录，不进项目依赖）。
> 凭据红线：本路纯前端 + 文档，无任何凭据面；过滤器串构建/解析均为纯函数，永不含凭据。
> 前序：P-LDAP（manifest 决策 + mock 桥 + 性能 + ldapi）已完成，基线前端 68 tests、go 四包全绿、S1-S10 10/10。

## 1. 交付总览

| 任务 | 状态 |
|---|---|
| 任务 1：多条件筛选构建器（对标 tiny-rdm ldapFilter.js） | **完成**（FilterGroup 递归树 + 嵌套组 ≤2 层 + 源码模式双向 + RFC 4515 解析器 + 表驱动 vitest） |
| 任务 2：双击打开条目 | **完成**（树节点双击 + 结果表行双击 → `ldap/entry/get` → EntryEditorDialog；右键菜单保留） |
| 任务 3：细节专业化 | **完成**（列头排序已有并保留 dn 默认升序；列宽拖拽 + localStorage 持久化；预设存结构化条件；树节点子条目数徽章 + loading 态；结果表 ↑/↓/Enter 键盘导航） |
| 任务 4：验证 | **全绿**（pnpm typecheck 干净 / **108/108** / build 通过；go vet + go test 四包零回归；mock 走查 8 张截图入 `docs/screenshots-a-ldap/`） |
| 禁 git commit/push / 无新依赖 / 七语齐全 | **遵守**（依赖零变更；playwright-core 装在 /tmp 未进 package.json；文案七语经 i18n.spec 断言） |

## 2. 任务 1：可视化条件构建器

### 2.1 lib/ldapFilter.ts 扩展（复用 tiny-rdm 移植底座）

> 对标说明：tiny-rdm `frontend/src/modules/tools/ldap/utils/ldapFilter.js`（UTF-16 编码）的 escape/substring/comparison/clause→group→builder 组装在 P 路前已完整移植为本文件上半部（escapeLdapFilterValue、build*Filter、combineFilters、validateLDAPFilter 等，既有 63 个用例继续覆盖）。本路新增下半部：

| 新增导出 | 说明 |
|---|---|
| `BuilderClause` / `BuilderGroup` / `BuilderNode` | 构建器树模型：clause（attribute/op/value/negate）+ group（join and|or/children/negate），组可嵌套（UI 限 ≤2 层） |
| `BuilderOp` = equals/contains/startsWith/endsWith/present/gte/lte/approx | 任务指定的 8 运算符，分别复用 buildEquality/Substring/Presence/ComparisonFilter |
| `createBuilderClause` / `createBuilderGroup` / `nextBuilderNodeId` | 工厂 + 自增 id（嵌套组增删的 key） |
| `buildNodeFilter` | 递归生成 RFC 4515 串：空行/空组跳过、单子项不包 `&(...)`（combineFilters 语义）、negate 经 buildNegatedFilter 防双否 |
| `collectBuilderErrors` | 每条件行校验（attribute_required/attribute_invalid/value_required），驱动「搜索」按钮禁用与行内错误文案 |
| `parseFilterStructure` / `parseClauseItem` / `unescapeLdapFilterValue` | **源码串→结构解析器**（尽力而为）：&/|/! 递归下降、presence、三种子串形态（`*v*`/`v*`/`*v`）、`>=`/`<=`/`~=`、`\XX` 反转义、negate 折叠为节点标记；**不可表示形态（多星子串如 `a*b`、`:dn:` 扩展匹配、空值、非法属性描述、括号不平衡）返回 null → 调用方保持源码模式** |
| `toBuilderRoot` | 根归一化：裸 clause 包一层 and 组 |
| `reviveBuilderNode` | 持久化结构（预设 conditions）的形状校验 + **id 重生成**（防与活动树撞 key） |

### 2.2 组件

| 文件 | 说明 |
|---|---|
| `components/FilterGroup.vue`（新增） | 递归条件组渲染器：AND/OR 切换、添加条件/添加组（`depth < 1` 才显示添加组 → 根+1 层嵌套上限）、删除行/组、属性 input 绑 `<datalist>`（schema 常用属性下拉）、present 时隐藏值输入 |
| `components/SearchForm.vue`（重构） | 过滤器区块双模式：**条件构建**（FilterGroup 树 + 实时预览串 `.qb-preview`）/ **源码**（RFC 4515 串直编）。构建→源码：填入生成串；源码→构建：parseFilterStructure 成功才切换，失败显示 `search.builderParseFailed` 并**保持源码模式**。产出 filter 串直接进 `ldap/search`（SearchFormModel.filter 契约不变） |
| 属性下拉 | `useLdapSchemaCache`（30min TTL 进程缓存）加载 `ldap/schema` 属性名 + 18 个常用属性兜底，注入 `<datalist>`；schema 加载失败静默降级 |

### 2.3 表驱动 vitest（`ldapFilter.spec.ts` 24→63 用例）

- **每运算符 × 特殊值转义**：equals/contains/startsWith/endsWith/gte/lte/approx ×（`a*b`/`a(b`/`a)b`/`a\b`/NUL/Unicode），present 恒 `attr=*`。
- **嵌套组**：`&(a)(|(b)(c))` 组合、空组剔除、整组取反一次、collectBuilderErrors 带 node id。
- **解析表**：14 个合法串→期望结构（含 `\2a`/`\5c`/`\28\29` 反转义、嵌套 negate）；15 个非法/不可表示串→null（多星、`**`、`:dn:`、空值、尾部内容等）。
- **源码模式往返**：6 棵树 build→parse→build 幂等（含 negate 子句/组、比较、近似、嵌套 OR-in-AND）。
- `reviveBuilderNode`：持久化恢复 + id 重生成 + 4 类畸形 payload 拒绝。

## 3. 任务 2：双击打开条目

| 入口 | 实现 |
|---|---|
| DN 树节点双击 | `TreeBranch.vue` 根节点按钮 `@dblclick`（twist 上 `@dblclick.stop` 防误触）→ `view` 事件链 → App `openEntry(dn)` → `ldap/entry/get`（仅该 DN 属性，屏蔽属性由后端照常过滤）→ EntryEditorDialog；右键菜单「查看/编辑」保留 |
| 树关键字过滤列表双击 | `DnTree.vue` filter-list 行 `@dblclick.stop` → 同上 |
| 结果表行双击 | 既有 `@dblclick` → `open` → `openEntry`（本路走查证实并留档截图 04） |

## 4. 任务 3：细节专业化

| 项 | 实现 |
|---|---|
| 结果表列头排序 | 既有实现保留（dn 列默认升序，点击切换/换列），本次补键盘联动（排序后选中随 sortedEntries 移动） |
| 列宽拖拽持久化 | 每个列头右缘 `.col-resize` 拖拽柄（pointer 事件，min 60px），按**列名**记录（`ldap.result.columnWidths.v1`），localStorage 持久化，mount 恢复；无宽度列走默认 minmax |
| 预设结构化条件 | `LdapSearchPreset` + `conditions?: unknown`；保存时构建器模式一并存树（JSON 克隆）；应用时 `reviveBuilderNode` 优先、退化 `parseFilterStructure(filter)`、再退化源码模式。走查：存→切源码改成 `(objectClass=*)`→应用→构建器恢复嵌套树→直接搜索 50 行 |
| 树节点徽章 | `DnTreeNode.childCount`（scope=one 拉取的子节点数，懒加载完成后常显、折叠也显示）；节点 loading 时显示 `…` 徽章 + twist 转圈。语义沿袭 P 路：sizeLimit 500 截断值 |
| 键盘导航 | `.result-table` tabindex + `@keydown`：↑/↓ 在**全量排序结果**上移动选中（跨页自动翻页 + scrollIntoView），Enter 打开条目（走 `ldap/entry/get`） |

## 5. 任务 4：验证

| 验证 | 结果 |
|---|---|
| `pnpm typecheck` | 干净 |
| `pnpm test` | **108/108**（基线 68 + 本路 40：ldapFilter.spec 63 含新增 builder/parse/roundtrip，i18n.spec 七语 key 集合一致性含 21 个新 key） |
| `pnpm build` | PASS（ui/index.html 自包含出包） |
| `go vet` + `go test -count=1 ./...` | 四包 ok，**零回归**（backend/** 零改动） |
| mock 走查（headless Chrome 1440×900，vite dev mock.html） | 见 §6；核心数据：嵌套组预览 `(&(cn=*user 1*)(|(mail=*@demo.internal)(uid=user00*)))`；搜索 50 行；双击行开 `uid=user0001`；键盘 ↓×3+Enter 开 `uid=user0012`；列宽 `{dn:379}` reload 后仍 `379px`；树双击开 `cn=ldap`；预设条件恢复后 50 行 |

## 6. 截图清单（docs/screenshots-a-ldap/，浏览器 1440×900 深色主题）

| # | 文件 | 验证点 |
|---|---|---|
| 01 | filter-builder-nested-group.png | 构建器嵌套组（根 AND + 子 OR 组、属性/运算符/值行、实时 RFC 4515 预览、树节点徽章 3） |
| 02 | results-nested-filter.png | 嵌套过滤器搜索结果（50 行/3 页） |
| 03 | source-mode-roundtrip.png | 源码模式：源码框内为构建器生成的串（双向切换） |
| 04 | double-click-opens-entry.png | 结果表行双击 → EntryEditorDialog（uid=user0001 表单态） |
| 05 | keyboard-navigation.png | ↑/↓ 选中行高亮（Enter 打开已用 editorDn 断言） |
| 06 | column-resize-persisted.png | dn 列拖宽至 379px，reload 后 gridTemplateColumns 生效 |
| 07 | tree-child-count-badges.png | 树徽章：dc=demo 3、懒展开 ou=people 500（sizeLimit 截断语义） |
| 08 | preset-structured-conditions.png | 预设存结构化条件并恢复（嵌套树 2 行 + AND/OR 态） |

> 走查方法说明：共享 MCP 浏览器被并行会话占用，本路改用独立 headless Chrome（playwright-core，装于 /tmp/a-ldap-walk，非项目依赖）驱动 mock.html，全部断言（editorDn/widths/preview/preset）为脚本返回值实测。

## 7. i18n 七语（en/zh-CN/zh-TW/es/it/ja/pt-BR）

新增 21 key × 7 语（i18n.spec key 集合一致性 + 非空断言护航）：

- `search.modeBuilder/modeSource`（条件构建/源码）
- `search.builderJoinAnd/builderJoinOr`（组 AND/OR 提示）
- `search.builderAddRow/builderAddGroup/builderRemoveRow/builderRemoveGroup`
- `search.builderAttrPlaceholder/builderValuePlaceholder/builderEmptyGroup`
- `search.builderParseFailed/builderAttrRequired/builderValueRequired`
- `search.opEquals/opContains/opStartsWith/opEndsWith/opPresent/opGte/opLte/opApprox`
- `result.keyboardHint`（↑/↓ 选择、Enter 打开）

## 8. 改动清单（本路）

| 文件 | 改动 |
|---|---|
| `frontend/src/lib/ldapFilter.ts` | +构建器树模型/递归构建/校验/RFC 4515 解析器/revive（约 +230 行，纯函数） |
| `frontend/src/lib/ldapFilter.spec.ts` | +表驱动用例（24→63） |
| `frontend/src/lib/dnTree.ts` | DnTreeNode +childCount |
| `frontend/src/lib/api.ts` | LdapSearchPreset +conditions |
| `frontend/src/components/FilterGroup.vue` | 新增：递归条件组 |
| `frontend/src/components/SearchForm.vue` | 重构：双模式过滤器区块 + 预设结构化条件 |
| `frontend/src/components/TreeBranch.vue` | 双击 view、childCount/loading 徽章 |
| `frontend/src/components/DnTree.vue` | childCount 接线、filter-list 双击、view 转发 |
| `frontend/src/components/ResultTable.vue` | 列宽拖拽持久化、键盘导航 |
| `frontend/src/lib/i18n.ts` | 七语 +21 key |
| `frontend/src/style.css` | wb-*/CSS 变量体系内新增 qb-*（构建器）、col-resize、徽章 loading 态样式（双主题经 var 继承） |
| `docs/screenshots-a-ldap/*.png`（8 张） | 走查留档 |
| `docs/PROGRESS-A-LDAP.zh-CN.md` | 本文档 |

backend/** 零改动；无 git commit/push；package.json 依赖零变更。

## 9. 遗留

1. **源码→构建器解析的不可表示形态**（多星子串 `a*b`、`:dn:` 扩展匹配等）按设计保持源码模式（源码为唯一权威表示）；未做「部分解析+保留原始片段」的混合模式。
2. **树徽章计数**沿袭 P 路语义：sizeLimit 500 截断值（ou=people 显示 500 而非 1000），精确计数需后端 `ldap/count` 能力，本路未扩契约（backend 只读约束）。
3. **列宽持久化按列名**：不同搜索结果列集不同，未见列不应用宽度（设计行为）；未做列显隐配置（tiny-rdm AgGrid 有，属新功能面）。
4. **浅色主题截图**未单独留档（样式全部走 CSS 变量，主题切换经 mock `?theme=light` 可验；P 路 09 已有浅色基线）。
5. 共享 MCP 浏览器并发会话抢占导致中途两次走查假失败（环境问题，非代码缺陷），已用独立浏览器排除并全部复验通过。

## 10. 阻塞

无。

---

## 11. 2026-08-29 追加：完善实施（配置显隐/必填核查 + smoke 复跑 + UI review）

> 同日追加小节。范围：manifest 显隐/必填契约核查与修复、真实容器 smoke 全量复跑、前端 UI review + mock 只读态实测。无 git commit/push，无新依赖。

### 11.1 manifest 配置项显隐/必填核查（对照宿主求值语义）

核查方法：对照宿主 worktree `pluginFieldConditions.ts`（单条件 `one_of`；引用字段取「当前表单值，隐藏字段保留 default」；保存时 visible && required && 空 → 阻止保存并提示缺失字段）逐项核对 manifest.json 与后端消费（lifecycle.go / dial.go / service.go / auth_gssapi.go）。发现并修复 5 处：

| # | 问题 | 修复 |
|---|---|---|
| 1 | `krb_password` visible_when 挂在 `krb_credential_type=password`：krb_credential_type 隐藏时仍保有默认值 password，导致**所有 auth_type 都显示 Kerberos 密码框** | 改为 `auth_type ∈ [kerberos]`，description 注明仅 credential_type=password 时使用（宿主单条件语义无法表达 AND 组合，取严格更优侧） |
| 2 | `bind_password` 未覆盖 ntlm：NTLM 需要 username+password（dial.go NTLMBind），表单却不显示密码框 | visible_when / required_when 补 `ntlm` |
| 3 | `bind_password` 对 digest_md5 缺 required_when | required_when 补 `digest_md5` |
| 4 | `ntlm_hash` 字段缺 required_when | required_when 补 `ntlm_hash` |
| 5 | `bind_dn` 仅 simple 可见：unauthenticated bind 的语义就是「DN + 空密码」，此前无入口 | visible_when 补 `unauthenticated` 并加 required_when |

同时：`username` 补 required_when（ntlm/ntlm_hash/digest_md5；此三态 bind_dn 隐藏且宿主对 plugin provider 不渲染标准 username 输入，username 是唯一入口）。保持 optional 不动的项及理由：`sasl_host`（缺省回退 URL 逻辑主机，resolveLDAPSASLHost）、`krb5_conf_path`/`krb_realm`/`krb_kdc_host`（可内联生成临时 krb5.conf 或回退 /etc/krb5.conf）、`tls_*` 全局项（对 ldaps/StartTLS 同样生效，非 use_starttls 专属）、allowed_base_dns / allowed_write_base_dns / read_only（与 policy.go 回退语义一致）。

契约测试（backend/internal/ldapconn/manifest_contract_test.go）新增 3 个：`TestManifestAuthTypeVisibilityMatrix`（8 场景 + kerberos keytab/ccache 2 子态，按宿主语义复现显隐集合逐一断言）、`TestManifestAuthTypeRequiredMatrix`（各 auth_type 空表单下「必填缺失集合」逐一断言，含静态必填项 default 非空校验）、`TestManifestSecretBindingsNeverPersistedAsConfig`（bind_password/ntlm_hash/krb_password 必须 binding=secret）。

### 11.2 真实环境 smoke 实跑（bitnami/openldap，随机一次性 admin 密码仅经环境变量）

| 套件 | 结果 |
|---|---|
| scripts/smoke_test.py（S1-S10 + S3b，sidecar 重建后二进制） | **11/11 PASS，0 FAIL，0 SKIP** |
| scripts/smoke_auth_test.py（T1-T5 + A1-A5，TLS 容器 + 临时自签证书） | **9 PASS + 1 SKIP（A2 DIGEST-MD5：OpenLDAP 容器无该 SASL 机制，预期 SKIP），0 FAIL** |

两套容器均由脚本自编排并在结束后 `compose down -v` 清理，无残留容器；密码不落盘。`go vet ./...` + `go test ./...` 四包全绿（ldapconn 含新增 3 契约测试）。

### 11.3 UI review（pnpm typecheck + vitest + build 全绿；浏览器实测 mock）

修复 2 处契约性缺陷 + mock 只读态补齐：

| # | 问题 | 修复 |
|---|---|---|
| 1 | `LdapConnectionStatus.state` 与后端契约 `status` 字段名不一致：ConnectionsPanel 状态点/文案**永远显示空闲** | api.ts 改 `status: "connected" \| "idle" \| "error"`；ConnectionsPanel 读取 `status.status` |
| 2 | `lastUsedAt` 声明为 ISO string，后端实为 unix 毫秒 number，面板时间显示为原始数字 | api.ts 改 `lastUsedAt?: number`；formatTime 兼容 number/string |
| 3 | mock `?ro=1` 下 `ldap/entry/add` 反而**跳过**白名单校验成功写入，modify/delete/modifyDn 完全无只读门禁 | mock 四个写方法统一 `readOnly → throw`（UI 层只读禁用之外的 fixture 保底） |

浏览器实测（playwright + vite dev，mock.html?ro=1）：顶栏「只读」徽章 ✓；树右键菜单 新增子条目/重命名/删除 均 disabled（查看/导出/复制 DN 可用）✓；双击打开条目编辑器：无保存按钮、「当前连接为只读」提示、全部输入控件 disabled ✓；连接面板显示「已连接」+ 格式化时间（验证修复 1/2）✓。非只读态回归：右键写入口全部可用 ✓。

### 11.4 本小节改动清单

| 文件 | 改动 |
|---|---|
| `manifest.json` | 5 处显隐/必填修复（§11.1 表） |
| `backend/internal/ldapconn/manifest_contract_test.go` | +required_when 结构字段；+3 个契约测试（显隐矩阵/必填矩阵/secret binding 红线） |
| `frontend/src/lib/api.ts` | LdapConnectionStatus：state→status、lastUsedAt→number |
| `frontend/src/components/ConnectionsPanel.vue` | 读取 `status.status`；formatTime 兼容 unix ms |
| `frontend/src/mockDbxHost.ts` | statuses 返回对齐契约；只读态写方法统一拒绝 |
| `docs/PROGRESS-A-LDAP.zh-CN.md` | 本小节 |

未新增任何用户可见文案（i18n 无改动，i18n.spec 七语断言不变）；`ui/index.html` 为 build 产物（不入库）。

### 11.5 遗留（追加）

1. **krb_password 显隐粒度**：宿主 visible_when 不支持组合条件（auth_type AND credential_type），现粒度为「kerberos 即显示」；keytab/ccache 用户会看到多余密码框（description 已注明）。宿主若支持 all_of 可收回。
2. **DIGEST-MD5 成功路径**（沿袭 XC 遗留 3）与**真机 Kerberos/NTLM** 集成仍需对应服务端。
3. 关键字过滤命中高亮（tiny-rdm 有）属新功能面，本轮按「只做小而确定修正」未加。

## 12. 2026-08-29 追加：第 3 轮对抗式审查与修复（LDIF/过滤器/缓存生命周期/UI）

对抗式找真实缺陷并修复，每项修复均有测试证据。基线：go 四包全绿、前端 vitest 108、
typecheck 绿；本轮后：前端 **126**（+18）、ldapconn 新增 3 缓存生命周期测试、
smoke S 11/11、T+A 9+1SKIP（与基线一致，无回归）。

### 12.1 问题清单与修复

| # | 严重度 | 问题 | 修复 | 证据 |
|---|---|---|---|---|
| 1 | 中 | **ldif.ts 解析器**：折叠注释（RFC 2849 §8 注释续行）被当作独立属性行，导入含折叠注释的第三方 LDIF 产生伪错误 `malformed line` | `unfoldLines`：注释续行并入注释逻辑行 | ldif.spec 新增 folded-comment 用例（修复前复现失败） |
| 2 | 中 | **ldif.ts 解析器**：条目内名为 `version` 的属性（如 `version: 9`）被 `/^version\s*:/i` 静默吞掉，导入丢数据 | 仅在尚未遇到 dn（`entry === null`）时按版本头跳过 | ldif.spec 新增 version-as-attribute 用例 |
| 3 | 中 | **ldapExporter.ts CSV**：pinned columns 走 `attrs[name]` 精确键查找，`columns: ["CN"]` 对 `cn` 键导出空单元格（与表头并集的大小写不敏感查找不一致） | 行内改走 `getEntryAttributeValues`（大小写不敏感） | ldapExporter.spec 新增 case-insensitive pinned columns 用例 |
| 4 | 高 | **EntryEditorDialog 表单模式保存丢真**：属性值本身含 `\n` 时，`entryToRows` join 后 `rowsToAttributes` 按 `\n` split 还原成两个值，保存即把 `["a\nb"]` 悄悄改成 `["a","b"]`（未编辑也触发 modify） | 提取纯函数 `attrRowsToAttributes`（ldapDiff.ts）：行文本与 `sourceValues.join("\n")` 一致的未编辑行原样保留原值数组，仅编辑过的行 split（去空段/去重）；EntryEditorDialog 接入 | 新建 ldapDiff.spec（多行值原样保留/编辑后 split/空名空值边界共 6 用例） |
| 5 | 中 | **ldapFilter.ts `unescapeLdapFilterValue`**：`\XX` 逐字节 `fromCharCode`，UTF-8 多字节序列（`\e4\b8\ad`）解码成 Latin-1 乱码 `ä¸­` 而非 `中`（服务器侧转义的非 ASCII 过滤值导入构建器即乱码） | 改为按转义游程收集字节 + `TextDecoder("utf-8")` 解码 | ldapFilter.spec 新增 UTF-8/NUL/混合用例；`a\2ab→a*b` 等原有 4 用例不变 |
| 6 | 中 | **service.go `Connect`**：同 id 重复 connect（连接编辑后重连，服务器/凭据可能已变）不断失效 schema 缓存，最长 10 分钟内 `ldap/schema` 仍返回上一台服务器的元数据（Disconnect 失效不覆盖此路径） | Connect 末尾统一 `invalidateSchema(profile.ID)`（幂等，首连为 no-op） | schema_test.go 新增 `TestSchemaCacheInvalidatedByConnectOverwrite`（修复前该测试逻辑必失败）+ disconnect 失效回归 + TTL/深拷贝/键隔离测试 |
| 7 | 低 | **DnTree.vue**：子节点懒展开失败把 `treeError` 置位后，模板 `v-else-if="treeError"` 分支**替换整棵树**——一次懒展开失败即吞掉整个已加载树 | 错误横幅与树并存（`treeError` 独立渲染）；懒展开成功清除横幅；失败节点保持未加载，再次点击即重试 | 模板重构；组件无测试基建（无 @vue/test-utils），行为经 mock 浏览器路径推演，见 §12.3 遗留 |
| 8 | 低 | **SearchForm.vue 预设重名**：未选中预设时输入已存在名称保存，会创建同名第二条预设（下拉不可分辨） | 未选中时按名匹配已有预设并沿用其 id 原地覆盖；选中态仍按所选 id 覆盖（含改名） | 逻辑内联一处；无组件测试基建，见 §12.3 |

过滤器对抗探测**未发现其他缺陷**（已固化行为）：对抗值（嵌 `*()\`、NUL、中文、
200 字长中文值）escape→parse→rebuild 往返稳定；深层 AND/OR/NOT 组合树与
reviveBuilderNode 持久化往返稳定；`a*b*c`/`a**b` 不可表示形状正确回退源码模式；
单子 AND 组折叠为叶子（`(&(cn=a))`→`(cn=a)`，语义等价的既有归一化，补测固化）。

LDIF 对抗往返（冒号/`<`/前后空格/换行/CR+LF/TAB/中英混排/emoji/500 字长行/
`#`开头/全空格/空值 + 非 ASCII dn）在修复 #1/#2 前后均**无损**，序列化器本身无缺陷。

### 12.2 验证值

| 项 | 命令 | 结果 |
|---|---|---|
| 前端 | `pnpm typecheck` / `pnpm vitest run` / `pnpm build` | 绿 / **126 passed（5→6 个 spec 文件）** / 产物写入 ui/index.html |
| 后端 | `go vet ./...` / `go test ./...` | 绿 / 四包 ok（ldapconn 含新增 3 个缓存生命周期测试） |
| 真机 | `scripts/smoke_test.py`（bitnami/openldap，随机一次性密码仅环境变量） | **11/11 PASS** |
| 真机 | `scripts/smoke_auth_test.py`（TLS 容器 + 临时自签证书） | **9 PASS + 1 SKIP（A2 无 DIGEST-MD5 机制，预期）** |

容器均 `compose down -v` 清理，无残留；密码不落盘。

### 12.3 本小节改动清单与遗留

改动：`frontend/src/lib/ldif.ts`、`ldif.spec.ts`、`ldapExporter.ts`、
`ldapExporter.spec.ts`、`ldapDiff.ts`、`ldapDiff.spec.ts`（新增）、
`ldapFilter.ts`、`ldapFilter.spec.ts`、
`frontend/src/components/EntryEditorDialog.vue`、`DnTree.vue`、`SearchForm.vue`、
`backend/internal/ldapconn/service.go`、`schema_test.go`、本文档。

遗留（发现未修/无法测）：
1. 表单模式对"值内含 `\n`"仍是**有损表示**：未编辑行已保真（#4），但用户一旦
   编辑该行文本，仍按行拆分——textarea 单格无法无歧义承载多值+值内换行，
   彻底方案需逐值编辑器（新功能面，超出"小而确定"）。
2. DnTree/SearchForm 两处 UI 修复无组件级自动化测试（工程无 @vue/test-utils
   依赖）；如后续补 UI 测试基建，应优先覆盖懒展开失败态与预设重名路径。
3. `escape`/`unescape`（ldif.ts base64 编解码）为 deprecated 全局函数，浏览器/Node
   均可用但长期宜换 TextEncoder 路径（纯重构，本轮不做）。
4. ResultTable 空态/排序/键盘导航通读未发现确定性缺陷，未改动。

### 多值歧义提示（2026-08-29 任务轮 4，遗留收口）

- §12 未修项 1 的最小改进入落地：值本身含换行的行在 EntryEditorDialog 标记
  `multiline` 并显示提示（编辑该行会按行拆分为多值），替代沉默歧义；七语
  `editor.multilineHint`。完整逐值编辑器仍属新功能面（未做）。
- 验证：go 四包绿、前端 vitest **126 passed**、typecheck/build 绿。

## 13. 2026-09-01 追加：runtime.host 完整 URL 容错（真机连接失败修复）

### 13.1 现象与根因

真机（AD `dc-apac.corp.int.kn`）连接报：
`dial ldap: LDAP Result Code 200 "Network Error": parse "ldaps://[ldaps:%2F%2Fdc-apac.corp.int.kn:636]:636": invalid URL escape "%2F"`。

根因是插件与宿主对 `runtime.host` 的契约错位：

- manifest 的 "LDAP URL" 字段 `binding: host`，按占位符约定填完整
  `ldaps://host:636`（这是**预期用法**，非用户误填）；
- 宿主直连路径（host 仓库 `dbx-core` `connection_host_port`）把
  `connection.host` **原文**透传进 `runtime.host`，无 scheme 剥离；仅配置
  transport layers（SSH 隧道等）时才是 `127.0.0.1:本地端口`；
- `dialProfile` 把 `runtime.host` 当裸主机名做 `net.JoinHostPort`，URL 被包进
  `[]` 且 `/` 转义为 `%2F`，得到不可解析目标。

### 13.2 修复与验证

- `dial.go` 新增 `normalizeDialHost`：拨号目标带 `://` 时按 URL 解析取
  hostname/port（端口为 0 时按 scheme 缺省），裸主机名与 `127.0.0.1` 传输端点
  原样放行，IPv6 字面量/畸形输入不受影响；D5 语义（隧道走本地端点）不变。
- 回归单测 `dial_target_test.go`：8 组表格用例 + 拼回 URL 必须可解析的钉子用例。
- 验证：`gofmt` 干净、`go vet ./internal/ldapconn/` 绿、
  `go test ./internal/ldapconn/` ok（含新增用例）。

### 13.3 本小节改动清单

`backend/internal/ldapconn/dial.go`、`dial_target_test.go`（新增）、本文档。
遗留：ssh 插件若存在同类「host 绑定即完整 URL」字段需另查（本轮未查）。

## 14. 2026-09-01 追加：连接表单结构化改造（host/port/tls_mode，v0.1.19）

### 14.1 背景

§13 修复后真机仍报同一错误：宿主里安装的 0.1.18 是 10:43 的**修复前同名构建**，
11:42 重构建的包未被重装（同版本号安装被跳过）。本次连同表单易用性一起改造，
并 bump 版本号 0.1.19 强制升级。

### 14.2 改动

- **manifest**（version → 0.1.19）：`url`（binding host）拆为三字段，对齐
  ssh 族约定——`host`（binding host，裸主机名，`127.0.0.1`）、`port`
  （binding port，389）、`tls_mode`（select config：none/starttls/ldaps，
  取代 `use_starttls`）；七语（en/zh-CN/zh-TW/ja/es/it/pt-BR）label 与
  tls_mode 选项全量补齐。
- **后端**：`service.go` 新增 `buildLDAPURL`——host 绑定为裸主机名时按
  tls_mode 组装 `scheme://host`（IPv6 字面量自动加 []，组装后 parse 校验，
  host:port 误填报错并指向端口字段）；旧连接的完整 ldap/ldaps/ldapi URL
  原样透传（StartTLS 仍取旧 `use_starttls`，ldaps+StartTLS 冲突保持 dial
  校验拒绝）。`NewProfileFromLifecycle` 改为三返回值（带 error）。
- **拨号容错保留**：`dial.go` `normalizeDialHost`（§13）继续兜底旧连接经
  runtime.host 透传的完整 URL。
- **测试**：`service_url_test.go`（组装/错误面/含 `tls_mode` 不被旧字段
  覆盖的回归钉子）、`manifest_contract_test.go`（字段矩阵换 host/port/
  tls_mode + 七语守卫扩展 + 旧字段必须移除）、smoke 新增 **T6**（结构化
  字段走真 ldaps 容器）。
- **文档**：IMPL_PLAN §4 字段表与 §10 风险项更新；smoke 注释同步。

### 14.3 验证

| 层 | 命令 | 结果 |
| --- | --- | --- |
| 后端 | `go vet ./...` / `go test ./...` | 绿 / 四包 ok（新增 15 个用例） |
| 前端 | typecheck / vitest / build | 全绿（无 UI 改动） |
| 打包 | `scripts/build.sh` | `dist/io.dbx.ldap-0.1.19-darwin-arm64.dbxp` |
| 真机 TLS | `smoke_auth_test.py`（自签证书容器） | **10 PASS + 1 SKIP**（A2 预期），含 T6 结构化字段 |
| 真机主 | `smoke_container.py`（S1–S10） | **11/11 PASS** |

容器均 `compose down -v` 清理；密码一次性随机生成仅经环境变量。

### 14.4 遗留

- 宿主侧未做任何改动（`runtime.host` 直连透传行为保留），插件侧双保险已覆盖。
- ssh 插件是否存在「host 绑定即完整 URL」同类形态仍未排查（与 §13 同）。

## 15. 2026-09-01 追加：工作台自动定位 Base DN（v0.1.20）

### 15.1 现象与根因

真机 AD（corp.int.kn:389）连接成功，但工作台空无一物并提示"连接未配置
Base DN"。根因：`App.vue` 的 `contextBaseDn` 只读连接表单 `base_dn`，为空时
目录树 `hasBaseDn=false` 直接空态；tiny-rdm 移植的主机名推断助手
（lib/baseDn.ts `inferBaseDnFromProfile`）是**死代码**，从未接线。

### 15.2 改动

- `lib/baseDn.ts` 新增纯函数 `pickBaseDnFromRootDse`：AD
  `defaultNamingContext` 优先 → `namingContexts` 里首个 `dc=` 项（跳过
  CN=Configuration/Schema、monitor）→ 首个 context。
- `lib/api.ts` `rootDse()` 支持显式 attributes（namingContexts/
  defaultNamingContext 属 operational 属性，缺省请求可能不下发）。
- `App.vue` 新增 `resolveAutoBaseDn`：显式 base_dn 优先 → RootDSE → 主机名
  推断（corp.int.kn → dc=corp,dc=int,dc=kn）；带竞态守卫（解析期间上下文
  带来显式值则放弃）；成功后 toast 提示实际生效值（七语 `tree.autoBaseDn`）。
- smoke 新增 **S11**（rootDse 显式 namingContexts 请求，钉住前端依赖的
  后端原语）；manifest version → 0.1.20。

### 15.3 验证

| 层 | 命令 | 结果 |
| --- | --- | --- |
| 前端 | vitest（baseDn.spec 新增 8 用例 + i18n 奇偶校验）| 13 passed |
| 前端 | typecheck / build | 绿 |
| 打包 | `scripts/build.sh` | `dist/io.dbx.ldap-0.1.20-darwin-arm64.dbxp` |
| 真机 | `smoke_container.py`（S1–S11） | **12/12 PASS**（含新 S11） |

容器 `compose down -v` 清理，密码一次性随机仅经环境变量。

### 15.4 备注

- 配了 `allowed_base_dns` 时 RootDSE 读取按策略拒绝（schema.go
  rootDSEAllowed），自动定位自动退回主机名推断；显式 base_dn 永远优先。
- 多命名上下文（AD Configuration/Schema）默认不进目录树根，可用
  搜索表单手填 baseDn 覆盖。

## 16. 2026-09-01 追加：工作台树/搜索交互修复（v0.1.21）

### 16.1 现象与根因

真机反馈：做子树搜索、条件搜索后「左边一下子就没了整棵树，无法还原」。
根因两处：

1. `App.vue` `searchHere`（树右键「在此搜索」）直接 `baseDn.value = dn`，
   而 DnTree watch `baseDn` 变化即整树重根——原根（dc=corp,…）被替换成被点
   节点的子树，且无任何还原途径。`baseDn` 同时承担「树根」与「搜索 base」
   两个语义是设计缺陷。
2. 树顶关键字过滤激活时左栏切换为平铺匹配列表，只有 Esc/清空输入框才恢复
   且无显式入口，观感即"树不见了"。

### 16.2 改动（tiny-rdm 语义：树常驻、搜索联动）

- `searchHere` 只把搜索面板 base 指向被点节点（`applyBaseDn`），树根恒为
  连接 Base DN，不再重根；
- `selectEntry`（点选树节点）联动搜索面板 base（tiny-rdm 搜索框跟随选中
  节点语义），替代原 no-op；
- DnTree 过滤框激活时显示显式 ✕ 清除按钮（恢复目录树），七语
  `tree.clearFilter`；懒展开失败已有"错误横幅与树并存"语义保持不变。
- 未做（登记）：搜索结果"在树中定位"需逐级异步展开祖先链，属新功能面。

### 16.3 验证

| 层 | 命令 | 结果 |
| --- | --- | --- |
| 前端 | vitest 7 文件 134 用例（i18n 奇偶含新 key） | 全绿 |
| 前端 | typecheck / build | 绿 |
| 打包 | `scripts/build.sh` | `dist/io.dbx.ldap-0.1.21-darwin-arm64.dbxp` |
| 真机 | `smoke_container.py` S1–S11 | **12/12 PASS**（sidecar 未改动，回归确认） |

### 16.4 备注

`baseDn`（树根）现在只由连接上下文（显式 base_dn / RootDSE / 主机名推断）
写入；搜索 base 只存在于 SearchForm 表单内。两语义解耦后，"在此搜索/点选
联动/表单手改"均不再影响目录树。

## 17. 2026-09-01 追加：搜索表单交互打磨（v0.1.22）

### 17.1 反馈与改动

| 反馈 | 根因 | 改动 |
| --- | --- | --- |
| 搜索按钮太大 | `.primary-button svg` 未限尺寸（lucide 默认 24px）+ 28px 高 | 新增 `compact` 变体（24px 高 / 12px 图标 / 11px 字号），仅搜索按钮使用 |
| 报错提示不友好 | 横幅直接透传 sidecar 原始串（`LDAP Result Code 49 "Invalid Credentials": …`） | 新增 `lib/ldapErrors.ts`：按结果码/传输错误映射 13 类可行动文案（认证失败、条目不存在、DN 语法、TLS 证书、网络不通、超时、限制类…），七语 `err.*`；横幅/树内错误展示友好文案，原始串挂 `title` 悬停可查；未知错误原样透传 |
| 条件必须合法，应允许为空 | 构建器空子句报 `attribute_required`、源码空串判 invalid，`run` 被 `filterValid` 拦死 | 空条件 = 匹配全部：构建器「属性与值皆空」的子句不报错（半填仍报错）、源码模式空串合法；运行时回退 `(objectClass=*)`（`toModel`/App `runSearch` 原有兜底）；过滤块下新增七语提示「条件留空 = 匹配全部条目」 |

### 17.2 验证

| 层 | 命令 | 结果 |
| --- | --- | --- |
| 前端 | vitest（新增 ldapErrors.spec 7 用例 + ldapFilter 空条件用例） | 全绿（141 用例） |
| 前端 | typecheck / build | 绿 |
| 打包 | `scripts/build.sh` | `dist/io.dbx.ldap-0.1.22-darwin-arm64.dbxp` |
| 真机 | `smoke_container.py` S1–S11 | **12/12 PASS**（sidecar 未改动） |

## 18. 2026-09-04 追加：宿主 1.1 theme 通道主题同步

宿主 `dev/plugin-framework-current`（cd3ee5a45，2026-09-03）向沙箱推送
`PluginBridgeTheme { appearance, tokens }`：init 携带 + env 消息实时推送，tokens
为宿主根节点解析后的 `--color-*` 设计令牌；`api.appearance` 契约在当前宿主恒
缺失（宿主侧 `pluginAppearance` 未接线）。工作台跟随宿主明暗与调色板实时切换：

- `env.d.ts`：新增 `DbxPluginTheme` 与 `DbxPluginApi.theme?`。
- `lib/hostTheme.ts`（新）+ `hostTheme.spec.ts`：token→colors 映射、
  `dbx-plugin-env` CustomEvent 订阅、输入校验（7 例单测）。
- `lib/appearance.ts`：`resolveAppearance` 入参放宽为逐字段可选
  `DbxPluginAppearanceInput`（theme 通道只带颜色令牌）。
- `App.vue`：init 时 `api.appearance` 缺失改用 `api.theme` 初始化；宿主无
  `onAppearanceChange` 时订阅 env 主题推送（退订入 `unsubscribeAppearance`
  数组，随卸载统一清理）。
- `mockDbxHost.ts`：按宿主形状镜像 `theme`（colors 反查 `--color-*` 令牌），
  `?theme=light|dark` 浏览器走查。

### 18.1 验证

| 层 | 命令 | 结果 |
| --- | --- | --- |
| 前端 | vitest（新增 hostTheme.spec 7 用例） | 全绿（171 用例） |
| 前端 | typecheck | 绿 |

sidecar 未改动，smoke 不受影响；未重新打包（下次 `scripts/build.sh` 随 build
重新生成 ui/）。本次无新增用户可见文案，七语无增量。

## 19. 2026-09-05 修复：详情弹窗背后的 filter 文本透出

### 19.1 现象与根因

设置可视化 filter 条件后双击结果行打开条目详情弹窗，弹窗四周（半透明遮罩
边缘）仍能清晰读到搜索表单区的 filter 构建器行与实时预览串
`.qb-preview`，观感上像文本"悬浮/污染"弹窗。

排查结论：层叠本身无缺陷——`.modal-backdrop` 为 `position:fixed; z-index:80`，
`elementFromPoint` 实测遮罩正常拦截全部下层内容，插件与祖先链均无创建层叠
上下文的属性；宿主 srcdoc 注入的 uiKit/SDK 亦不涉及层叠。根因是遮罩不透明
度过低（亮 25% 黑 / 暗 70% 背景色），背景文字透出可辨。

### 19.2 改动

- `style.css`：`.modal-backdrop` 亮色 `rgb(0 0 0 / 25%)` → `rgb(0 0 0 / 50%)`；
  暗色 `color-mix(... 70%, transparent)` → `92%, transparent`。弹窗打开时
  背景 filter 文本不再可辨，同时保留层叠纵深感。

### 19.3 验证

| 层 | 方式 | 结果 |
| --- | --- | --- |
| 视觉 | vite dev mock.html（880×660）：构建器设条件 → 搜索 → 双击行开弹窗，前后截图对比（暗/亮两主题） | 修复前 filter 行与预览串清晰可读；修复后不可辨 |
| 前端 | typecheck + vitest | 全绿（171 用例） |

ssh/files 两插件 `.modal-backdrop` 为同款低不透明度遮罩，如需统一观感可
随后跟进（未在本次改动范围内）。

## 20. 2026-09-05 追加：ADS 追赶 M5-a（排版联动 + ≠ 运算符 + UI 测试三轨）

对齐 Apache Directory Studio 的第一轮（对账表：
`docs/ADS_GAP_ANALYSIS.zh-CN.md`，路线并入 IMPL_PLAN §9 M5/M6）。

### 20.1 改动

- `manifest.json`：tls_verify / tls_ca_path / tls_server_name 增加
  `visible_when: tls_mode ∈ {starttls, ldaps}` 联动显隐，并把三个 TLS 字段
  移到「加密方式」之后——连接表单排版对齐 ADS 的 网络→加密→认证→高级
  分区；31 字段七语标签已全覆盖（校验过）。
- `lib/ldapFilter.ts`：`BuilderOp` 新增 `notEquals`（`(!(attr=val))`，值走
  RFC 4515 转义；空值返回空串）；`parseFilterItem` 的 `(!)` 分支把「否定
  等式」折叠为一等 ≠ 运算符（其余形态保留 negate 语义，往返稳定）；
  `reviveBuilderNode` op 白名单同步。
- `components/FilterGroup.vue`：运算符下拉加入 ≠（紧跟 = 之后，对齐 ADS
  顺序）。
- `lib/i18n.ts`：七语新增 `search.opNotEquals`（en/es/it/ja/pt-BR/zh-CN/zh-TW）。

### 20.2 UI 测试三轨（M5-a 交付）

| 轨 | 内容 | 结果 |
| --- | --- | --- |
| 纯函数 | `ldapFilter.spec.ts` 新增 notEquals describe 7 用例（构建/转义/组合/校验/解析折叠/往返/revive），并更新 1 处旧表示断言（equals+negate → notEquals） | 78/78 |
| 组件（happy-dom + @vue/test-utils，新增 devDeps） | `FilterGroup.spec.ts`（6 用例：运算符全集、v-model 双向、增删行/空组、presence 隐藏值格、disabled 门禁、嵌套上限）+ `SearchForm.spec.ts`（6 用例：空条件回退、实时预览、≠ 构建、半填拦截/放行 submit、构建↔源码往返、非法源码报错） | 12/12 |
| 浏览器 | 新增 `scripts/ui_test.mjs`（playwright-core 惰性装于 /tmp/dbx-ldap-ui-deps 非项目依赖 + 系统 Chrome，自拉 vite dev mock.html）：预览 equals/≠、mock 搜索出行、双击开详情弹窗、**暗色遮罩 ≥90% 不透明回归断言**、elementFromPoint 遮罩拦截、构建→源码带串。已接入 `scripts/test.sh`（无 Chrome/网络时 SKIP，断言失败 exit 1） | 7/7 |

### 20.3 验证

pnpm typecheck 干净；vitest 全量 **190/190**（14 文件）；`pnpm build` 通过
（ui/index.html 重新生成）；`node scripts/ui_test.mjs` 7/7。sidecar 未改动。
