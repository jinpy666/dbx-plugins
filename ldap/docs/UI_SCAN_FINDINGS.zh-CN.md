# LDAP 插件前端 UI 体验扫描报告（UI_SCAN_FINDINGS）

> 扫描角色：场景驱动 UI 体验扫描 agent（只读扫描 + 报告，不实施修复）。
> 扫描对象：`ldap/frontend`（mock.html 可视化夹具，Vue 3 工作台）。本报告只记录发现与方向建议，不含代码修改。

## 一、扫描环境

| 项 | 值 |
| --- | --- |
| 轮次 | 第 1 轮基线走查（2026-09-06，工作区无并行改动干扰） |
| Dev server | 本轮新起 `vite --port 5292 --strictPort`（ldap/frontend 工作目录，扫描结束已 kill），未跑 build |
| 自动化 | playwright-core 1.63 + 系统 Chrome（headless，channel=chrome），独立装于 `/tmp/uiscan-ldap`（未进项目依赖） |
| 视口矩阵 | 1280×900（默认）、720×900（窄，<900px 断点）、1440×900（宽） |
| URL 参数矩阵 | 默认（dark）/ `?theme=light` / `?ro=1` / `?err=1` / `?noconn=1` / `?locale=en`（夹具另支持 `?locale=`，均按源码逐一注入） |
| 交互核验 | Tab 顺序、Esc 关闭/否决、遮罩点击、Enter 提交、焦点陷阱与归还、按钮禁用态、表单校验、列宽拖拽持久化、虚拟滚动跳跃 |
| 数据规模 | 夹具含 1000 条 uid=user* 人员条目（ou=people），用于虚拟滚动/分页/截断验证 |
| 证据 | 截图 20+ 张（走查核对后已全部删除，未入库） |
| 控制台健康度 | 唯一反复出现的 console 噪音为 favicon 404；全程无未捕获 page error |

## 二、发现清单

统计：**P0 × 0，P1 × 2，P2 × 12**。无阻断使用级问题；主旅程（树→搜索→编辑→重命名→删除→导出）端到端可走通。

### P1（明显可感知的体验债）

**P1-1 条目编辑器有未保存修改时，点击遮罩直接关闭并丢弃修改（Esc 的防误丢保护被绕过）**
- 位置：EntryEditorDialog（编辑态与新增态）
- 复现：双击结果行打开编辑器 → 修改任一属性值（出现“有未保存的修改”）→ 点击弹窗外遮罩任意处 → 弹窗立即关闭；重开后修改全部丢失。新增模式同样：RDN 填到一半点遮罩即关。
- 期望 vs 实际：代码对 Esc 做了明确的 dirty 否决（`useModalA11y allowClose`，“防误丢”），但 `.modal-backdrop @click.self` 直接 `emit('close')`，未经过 `allowClose` 检查——同一风险两条关闭途径只有一条有保护，语义不一致。
- 影响：多字段编辑场景下一次误点（遮罩在 1280×900 下面积很大）即丢失全部输入，且无任何确认或提示。
- 建议：遮罩点击复用 `allowClose()` 判定（dirty 时否决或弹确认）；或 dirty 时禁用遮罩关闭，仅保留 ✕/取消为显式放弃入口。
- **修复状态（2026-09-06）：已修复，复验通过。** 遮罩点击经 `modal.ts` 新增的
  `decideBackdropClose` 决策与 Esc 共用同一条 `allowClose` 守卫（dirty/提交在途
  时静默否决，弹窗保持打开、输入不丢；✕/取消仍为显式放弃入口，干净态遮罩点击
  仍直接关闭）。单测：modal.spec 守卫决策 2 例 + EntryEditorDialog.spec 遮罩
  dirty 否决/新增态否决/干净态关闭 3 例。浏览器复验（playwright + 系统 Chrome，
  mock.html）：dirty 修改后点遮罩 → 弹窗保持打开且值保留；Esc 否决不回退；
  ✕ 显式关闭后重开为原值；干净态遮罩点击正常关闭，11/11 断言通过。

**P1-2 目录树懒加载静默截断 500 条，子条目徽标却显示精确总数（1000），无任何截断提示**
- 位置：DnTree（fetchChildren sizeLimit=500 + ldap/count 徽标）
- 复现：展开 ou=people → 徽标显示“1000”（ldap/count 精确值）→ 树实际只挂载 504 行（根层 4 行 + 500 子条目，vlist 总高 14112px / 28px），滚到底只有 uid=user0499，无“已截断/加载更多”任何提示。
- 期望 vs 实际：徽标承诺 1000 个子条目，树只给 500，且用户无从得知后 500 条存在。真实服务器上 >500 子条目的 OU 表现会更糟：要么 one 层搜索直接报 size limit exceeded（err 映射有对应文案），要么像夹具一样静默截断。
- 影响：目录浏览器核心是“看到全部条目”，静默丢一半且徽标反向背书完整性，属数据可见性缺陷。
- 建议：fetchChildren 感知截断（比较 count 与 children.length）时在节点行内显示“500/1000，已截断”徽标或“加载更多”入口；徽标与实际加载量不一致时至少给 title 提示。
- **修复状态（2026-09-06）：已修复，复验通过。** `DnTreeNode` 新增 `truncated`
  标记（`dnTree.ts` 提供单页常量 `TREE_FETCH_PAGE=500`、`isFetchTruncated`、
  `nextFetchLimit`、`childBadgeText` 纯函数）；截断时 TreeBranch 徽标显示
  “500+”（绝不背书精确总数），title/aria-label 给出“已加载 500 / 共 1000 个
  子条目，点击加载更多”（count 未回时降级为“已加载前 500 个…（已截断）”），
  点击徽标即续载一页（重取替换 children、保留已展开子节点状态；ldap/count
  精确总数负责纠正“恰好整页”的截断边界误判）。文案 `tree.childCountTruncated` /
  `tree.childCountTruncatedUnknown` 七语齐全。单测：dnTree.spec 助手 5 例 +
  TreeBranch.spec 徽标/续载事件 4 例。浏览器复验：展开 ou=people → 徽标 “500+”
  → 点击续载 → 徽标恢复精确 “1000”，树底部可达 uid=user0999。

### P2（打磨项）

**P2-1 `?err=1` 错误原文英文透传**：树错误区、顶部横幅、树内过滤错误三处均直接显示 `connection lost (fixture error injection)`；`friendlyLdapError` 的 network 规则只覆盖 refused/reset/no such host 等，”connection lost”类网络错误未命中映射，title 悬停与正文同串（无本地化/原文分离）。建议补网络类规则（如 `connection lost|connection closed`）。
- **修复状态（2026-09-06）：已修复，复验通过。** `ldapErrors.ts` network 规则补
  `connection lost|connection closed`（归入 err.network，本地化文案”无法连接
  LDAP 服务器…”），title 悬停保留英文原文供排查（设计语义不变）。单测
  ldapErrors.spec +1。浏览器复验：`?err=1` 树错误区/横幅均显示中文文案，
  title=”connection lost (fixture error injection)”。

**P2-2 `?noconn=1` 初始化失败态自相矛盾**：下方提示”LDAP 连接上下文未就绪…”，但工具栏 identity 仍显示完整连接身份 `cn=admin,…@ldap.demo.internal:389`，且加载 spinner（`v-if=”!ready”`）永久旋转。建议 init 失败后停转/隐藏 spinner，identity 降级显示占位。
- **修复状态（2026-09-06）：已修复，复验通过。** `App.vue` spinner 改
  `v-if=”!ready && !initError”`（init 失败即停转）；identity 由 `identityText`
  计算，initError 时降级为新七语 key `connectionPlaceholder`（”暂无活跃连接”），
  不再展示看似就绪的完整连接身份。浏览器复验：`?noconn=1` spinner 数 0、
  identity=”暂无活跃连接”。

**P2-3 空结果文案不区分”未搜索”与”搜索无匹配”**：执行 base=不存在的 DN 搜索 0 条后，仍显示”暂无结果，请先执行搜索”，对刚点过搜索的用户构成误导。建议区分两种空态文案。
- **修复状态（2026-09-06）：已修复，复验通过。** `App.vue` 记录 `hasSearched`
  传给 ResultTable，空态按 searched 分流：未搜索仍”暂无结果，请先执行搜索”，
  搜索后 0 条改显新七语 key `result.emptyNoMatch`（”当前搜索没有匹配的条目”）。
  新增 ResultTable.spec 3 例。浏览器复验：搜索不存在 DN 后显示”当前搜索没有
  匹配的条目”。

**P2-4 Schema 面板关键字无匹配时显示”Schema 为空”**：过滤”zzz”时两列均显示 `(0) Schema 为空`，语义是”schema 加载为空”而非”无匹配项”，易误判为 schema 拉取失败。建议无匹配时用”无匹配’{keyword}’”文案。
- **修复状态（2026-09-06）：已修复，复验通过。** `SchemaPanel.vue` 空列文案按
  关键字分流：有字时显示新七语 key `schema.noMatch`（”没有匹配”{keyword}”的定义”），
  无字时保留 `schema.empty`。SchemaPanel.spec 断言更新 + 新增 1 例。浏览器复验：
  过滤 zzz 两列均显示”没有匹配”zzz”的定义”。

**P2-5 过滤器非法时”搜索”按钮不联动禁用**：源码模式输入 `(uid=*0001` 后行内红字”LDAP 过滤器不合法”出现，但按钮仍可点（`run()` 内部 guard 拦截，点击无任何响应）。建议校验失败时 disabled（或点击时聚焦错误位置），与 kafka 生产面板校验联动收口方式对齐。
- **修复状态（2026-09-06）：已修复，复验通过。** `SearchForm.vue` 搜索按钮
  `:disabled` 加 `|| !filterValid`，禁用时 title 提示”LDAP 过滤器不合法”
  （行内红字保持）。SearchForm.spec +1（禁用/提示/恢复可点/提交拦截）。
  浏览器复验：非法源码过滤器按钮 disabled + title，恢复合法即解锁。

**P2-6 新增条目 RDN 无客户端语法校验**：RDN 填 `cn=bad,dn` 直接保存，弹”条目已新增”（夹具放行，真实服务器会报 invalid DN）。RDN 含逗号/空段/缺少 `=` 等可本地预检，提前拦截比等服务器报错友好。
- **修复状态（2026-09-06）：已修复，复验通过。** `EntryEditorDialog.vue` 新增态
  用 `dn.ts` 既有 `isLikelyRdn` 做客户端预检：非法 RDN（顶层逗号/缺 `=`/空值段）
  行内红字提示新七语 key `editor.rdnInvalid`，保存按钮禁用 + save() 二次守卫。
  EntryEditorDialog.spec +1（三类非法 RDN 拦截、合法恢复）。浏览器复验：
  `cn=bad,dn` 行内提示 + 保存禁用 + 不发请求。

**P2-7 选中树节点静默改写搜索 Base DN**：点击任意树节点后搜索表单 Base DN 即变为该节点 DN（tiny-rdm 跟随语义，行为本身有依据），但无任何视觉提示；用户点选节点浏览后点”搜索”，整树搜索悄然变成单条目搜索（实测 1008 条变 1 条）。建议 Base DN 被联动改写时做一次性高亮或 toast 提示。
- **修复状态（2026-09-06）：已修复，复验通过。** 选改动小的方案：`SearchForm
  applyBaseDn` 在值变化且 `highlight!==false` 时给 Base DN 输入框加 1.6s
  `.base-dn-flash` accent 描边高亮 + title 新七语 key `search.baseFollowed`
  （”Base DN 已跟随选中的树节点”）；初始化/自动定位回填传 `highlight=false`
  不触发。SearchForm.spec +1。浏览器复验：点选树节点 → 输入框高亮 + title，
  1.8s 后自动消退。

**P2-8 错误横幅位置遮挡主区顶部**：`.error-banner` fixed top:42px 居中，出现时盖住 Base DN/范围字段上沿（工具栏下 6px）。可关闭、瞬态，影响有限；建议参考 kafka 收口（下移让出表单区）。
- **修复状态（2026-09-06）：已修复，复验通过。** 采用报告建议的”主区下移”方案：
  横幅从 `position:absolute top:42px` 遮罩改为布局流内（模板移到工具栏与主区
  之间，CSS 改 static/align-self:center），出现时主区整体下移让位，不再遮挡
  任何表单字段。浏览器复验：横幅在工具栏正下方流内渲染，Base DN 输入框整体
  位于横幅下方，无重叠；关闭后布局还原。

**P2-9 light 主题工具栏连接色染色偏重**：默认连接色 #8b5cf6 以 10% alpha 染整条工具栏（`App.vue colorWithAlpha(color, 0.1)`），light 下呈明显淡紫，易与状态色混淆。kafka 已将同类染色收口至 5%，建议家族对齐。
- **修复状态（2026-09-06）：已修复，复验通过。** `App.vue toolbarStyle` 按
  `appearance.colorScheme` 分档：light 5%/0.12，dark 10%/0.18（与 kafka 完全
  对齐）。浏览器复验：`?theme=light` 工具栏背景 rgba(139,92,246,0.05)，
  淡紫明显减弱。

**P2-10 目录树无键盘导航**：树节点仅可 Tab 逐个聚焦（每节点 2 个停止位：twisty+节点），无 ↑/↓ 移动、无 aria-tree 语义；且虚拟滚动下未挂载行 Tab 永远不可达。结果表已有 ↑/↓+Enter 支持，建议树补方向键导航（roving tabindex）。
- **修复状态（2026-09-06）：已修复，复验通过。** 按”上下键在可见行间移动 +
  Enter 选中”收口：`dnTree.ts` 新增纯函数 `nextTreeFocusIndex`（相邻移动、
  边缘钳制、焦点在树外时从首/尾行进入、非方向键不接管），`DnTree.vue` 在
  `.tree-rows` 容器接 `@keydown`，↑/↓ 在当前渲染出的 `.tree-node` 间移动焦点
  并 scrollIntoView（Enter 由原生 button click 触发选中）。dnTree.spec +4。
  浏览器复验：焦点行 0 → ArrowDown → 1 → ArrowUp → 0，Enter 触发选中。

**P2-11 删除确认弹层初始焦点落在标题栏 ✕**：打开后 `document.activeElement` 为 ✕（关闭）按钮。✕ 亦是非破坏动作、风险低，但惯例是聚焦”取消”，避免键盘用户 Enter 误触发的路径经过关闭图标。
- **修复状态（2026-09-06）：已修复，复验通过。** `modal.ts useModalA11y` 新增
  `initialFocus` 选项（弹层内 CSS 选择器，缺失回退首个可交互控件）；
  `DeleteEntryDialog` 传 `initialFocus: “footer button”`（取消）。新增
  DeleteEntryDialog.spec 初始焦点 1 例（真实挂载断言 activeElement）。浏览器
  复验：打开删除确认 → activeElement 为”取消”按钮。

**P2-12 mock 页 favicon 404**：每次加载 console 报一条 404（kafka 已内联 data-icon 收口同类问题，ldap mock.html 未做）。
- **修复状态（2026-09-06）：已修复，复验通过。** `mock.html` 补内联 SVG
  data-icon（对齐 kafka 收口方式，LDAP 目录树图形）。浏览器复验：全程 console
  无 favicon 404（0 条 console error）。

### 观察项（行为符合设计，记录备查）

- 右键菜单“在此搜索”与单击选中行为等价（都只联动搜索 Base，不改树根）——与 tiny-rdm 语义一致。
- 树懒展开失败时错误横幅与树并存、节点保持折叠可重试；根失败才整树错误态——语义合理。
- 写操作后整树 reload（modifyDn 后 `invalidate()` 无参）：深层节点重命名后树折叠回根、位置丢失。当前夹具层级浅影响小，真实深层目录下可感知，建议后续改为局部 invalidate。

## 三、场景走查矩阵

| 场景 | 结果 | 备注 |
| --- | --- | --- |
| 端到端主旅程（树展开→过滤→搜索→排序/翻页→编辑保存→新增→重命名→删除→导出） | ✓ | 全链路无阻断；notice/error 反馈齐全 |
| 虚拟滚动（1000 子条目树） | ✓/✗ | 跳跃滚动无空洞、选中态跨回收保留；但 500 截断（P1-2） |
| 结果表大数据（1008 条 / 21 页） | ✓ | 排序稳定、跨页键盘导航（↑/↓/Enter 打开对应条目）正确、末页内容正确 |
| 列宽拖拽 | ✓ | 拖拽生效 + localStorage 持久化 + 刷新保留（`ldap.result.columnWidths.v1`） |
| 过滤器构建器 | ✓ | AND/OR 切换、预览串、属性非法校验（“每个条件都需要合法的属性名”）、空条件=匹配全部 |
| 源码模式双向切换 | ✓ | 串→结构尽力解析，解析失败保持源码模式并提示（设计正确） |
| 预设保存/应用/删除 | ✓ | 结构化条件随预设恢复；重名原地更新逻辑在 |
| EntryEditor 三形态 | ✓/✗ | 查看/编辑/新增、LDIF 双向同步、非法 LDIF 拦截、dirty Esc 否决均好；遮罩绕过（P1-1） |
| ModifyDnDialog | ✓ | 预填 RDN、空 RDN 禁确认、Enter 提交、提交在途否决关闭 |
| DeleteEntryDialog | ✓ | 红色警示样式、DN 全文展示；初始焦点 ✕（P2-11） |
| SchemaPanel | ✓/✗ | 加载/过滤/刷新/Esc 正常；无匹配文案误导（P2-4） |
| ConnectionsPanel | ✓ | 状态点 + 最近使用时间 + Esc 关闭 + 焦点归还触发按钮 |
| 右键菜单 | ✓ | 视口边缘夹紧、↑/↓ 键盘移项、Esc 关闭并归还焦点、ro 下写项禁用 |
| `?err=1` | ✗ | 三处英文原文透传（P2-1）；横幅遮挡（P2-8）；可关闭、树错误可恢复 |
| `?ro=1` | ✓ | 只读徽标、菜单写项禁用、编辑器“当前连接为只读”+ 输入禁用 + 无保存按钮；“在此搜索”仍可用 |
| `?noconn=1` | ✓/✗ | 本地化文案清晰、工具栏全禁用；identity+spinner 矛盾（P2-2） |
| `?theme=light` | ✓ | 明暗两套渲染正常无拼色；muted 文本对比度 4.74:1、identity 4.68:1（均 ≥4.5）；工具栏染色偏重（P2-9） |
| 720×900 | ✓ | <900px 断点纵向堆叠（树 42% 高）、工具栏按钮收缩为图标、无水平溢出 |
| 1440×900 | ✓ | 树 30% / 主区自适应，无溢出 |
| `?locale=en` | ✓ | 全量英文、无中文 key 泄漏（七语机制抽查通过） |
| AuditFeedPanel | ⚠️ 未验证 | 夹具仅在 ro 写拒绝时发 denied 事件，而 ro 下 UI 已预禁用全部写入口，denied 路径在 mock 中不可达；ok 事件夹具不产生。需真机或补夹具 |

## 四、验证与遗留

- 走查脚本 6 组（主旅程 ×3、状态矩阵、细节 ×2），全程无未捕获 page error；发现条数 P0=0、P1=2、P2=12。
- 走查用截图已全部删除，未入工作区；`/tmp/uiscan-ldap` 下脚本为扫描工具产物，不入库。
- 遗留未验证（夹具限制）：① AuditFeed denied/ok 事件到达时的自动展开与高亮；② 连接切换（onContextChange 换连接）后的树/表单重置；③ typesOnly 在夹具中无行为差异；④ 真实服务器 >500 子条目 OU 的实际表现（错误 or 截断，对应 P1-2）。

## 五、收口状态（实施侧回填）

**P1 修复轮（2026-09-06）**：P1-1、P1-2 已全部修复并浏览器复验通过（复验
结论见各条目"修复状态"附注）。修复同时把基线上 6 个既有失败用例归零
（modal `useModalA11y` 补 `immediate` 挂载、AuditFeedPanel denied 自动展开
语义、EntryEditorDialog.spec mock 计数重置、SchemaPanel.spec 两处对齐）。

**P2 修复轮（2026-09-06）**：P2-1 ～ P2-12 已全部修复并浏览器复验通过
（playwright-core + 系统 Chrome，mock.html，26/26 断言通过，截图即验即删）。
ModifyDnDialog / DeleteEntryDialog 遮罩已接入 `decideBackdropClose` 共享守卫
（各有提交在途否决语义，顺手收口）；SchemaPanel / ConnectionsPanel 遮罩仍
`@click.self` 直关（无 dirty/在途守卫语义，接入与否行为等价，留作 optional）。

| 项 | 状态 |
| --- | --- |
| P1-1 遮罩 dirty 丢改动 | ✅ 已修复（modal.ts `decideBackdropClose` 共享守卫 + EntryEditorDialog 接线 + 5 例单测） |
| P1-2 树懒加载静默截断 | ✅ 已修复（truncated 标记 + "500+"徽标即加载更多入口 + 七语文案 + 9 例单测） |
| P2-1 ～ P2-12 | ✅ 已全部修复（第 2 轮，2026-09-06；方式与复验结论见各条目"修复状态"附注） |
| 验证（第 2 轮） | `pnpm typecheck` 0 错；`pnpm test` 23 文件 279 用例全绿（265 → 279）；浏览器复验 26/26 断言通过（playwright-core + 系统 Chrome，mock.html，截图已删未入库） |
| 同类隐患备忘 | SchemaPanel / ConnectionsPanel 的遮罩仍是 `@click.self` 直关——无 dirty 态、无否决语义，接入 `decideBackdropClose` 行为等价，按 optional 跟进 |

## 六、第 3 轮（专家视角深度测试，2026-09-06）

> 视角：资深 LDAP 目录服务管理员（OpenLDAP/AD）+ 软件测试专家，聚焦性能/压力、
> LDAP 语义正确性、健壮性、易用性、i18n/a11y 五个维度；不重复第 1/2 轮已收口项。
> 只记录，不改代码。本轮全程 0 未捕获 page error、0 console error（favicon 已收口）。

### 环境表

| 项 | 值 |
| --- | --- |
| 轮次 | 第 3 轮专家视角（2026-09-06，工作区无并行改动） |
| Dev server | 本轮新起 `vite --port 5292 --strictPort`（ldap/frontend，结束已 kill），未跑 build |
| 自动化 | playwright-core 1.63 + 系统 Chrome（headless，channel=chrome），装于 `/tmp/uiscan-ldap-r3`（未进项目依赖） |
| 竞态注入 | `addInitScript` 包裹 `window.dbxPlugin.invoke`：按方法注入延迟/一次性失败（不改业务代码） |
| 数据规模 | 夹具 1008 条（ou=people 1000 条人员 + 根/OU/服务/组），结果表 21 页、树 1000+ 行 |
| i18n 核查 | 脚本解析 `i18n.ts`，对七语（en/zh-CN/zh-TW/es/it/ja/pt-BR）做 key 集合对比 + 值语言泄漏扫描 + en/ja/zh-TW 术语抽查（20 个点） |
| 证据 | 全部为断言输出（未截图）；`/tmp/uiscan-ldap-r3/*.mjs` 为工具产物，不入库 |

### 新发现清单

统计：**P0 × 0，P1 × 1，P2 × 9**。主旅程与回归矩阵无回退。

#### P1（数据可见性/完整性缺陷）

**P1-3 右键「导出子树（LDIF）」硬编码 sizeLimit=500 且忽略 `truncated` 标记，>500 条目的子树静默导出残缺**

> R4 修复标注（2026-09-06）：已修复——导出上限提至 5000 并消费 truncated；仍截断时通知明确告知「仅导出前 N 条」（result.exportTruncated 七语）。浏览器复验通过。
- 位置：`App.vue` `exportSubtree()`（`sizeLimit: 500, pageSize: 500` 固定值；`result.truncated` 未读取）
- 复现：右键 `ou=people`（子树共 1001 条）→ 导出子树 → 抓取导出 blob：**恰好 500 行 `dn:`**（含 ou=people 自身 + 499 个 user，`user0499`/`user0500`/`user0999` 均缺失），通知「已导出 LDIF」，全程无任何截断提示（结果区截断徽标只在搜索路径出现，导出路径不经过）。
- 影响：管理员拿这个 LDIF 做备份/迁移/审计时会拿到一半目录且毫无察觉——比 P1-2（树内截断，至少有徽标）更隐蔽。契约里 `LdapSearchResult.truncated` 就是为此设计的，导出路径没有消费它。
- 建议：exportSubtree 检查 `result.truncated`（或按 pageSize 循环拉全量），截断时横幅警告或在通知中带「已截断（500/1001）」；导出前可提示预计条数（ldap/count 已有）。

#### P2（打磨项）

**P2-13 LDIF 模式可编辑 DN 行，但保存语义是错的（rename 不走 modify）**

> R4 修复标注（2026-09-06）：已修复——编辑态 LDIF 的 dn 变更被忽略并显示锁定提示（editor.ldifDnLocked 七语），保存仍发往原 DN；仅改 dn 无属性 diff 时通知「没有需要保存的修改」而非静默关闭。单测 2 例 + 浏览器复验通过。
- 位置：`EntryEditorDialog.vue`（LDIF 双向同步 `syncRowsFromLdif` 直接信任解析出的 `entry.dn`）
- 复现 A（只改 DN 行）：编辑态切 LDIF → 把 `dn:` 改成别的 DN → 保存 → 属性 diff 为空 → `emit("close")` 弹窗**静默关闭，零反馈**（无"已保存"、无错误、无任何变化）。复现 B（改 DN + 改属性值）：保存 → modify 发往新（不存在的）DN → 横幅报 `entry not found: uid=ghost,…`（mock 原文；真机 sidecar 为 code 32，会映射成本地化"条目不存在"，但用户仍会困惑：我改的是这个条目，为什么找不到）。
- 影响：把"LDIF 里的 dn 是可编辑文本"误当成 rename 入口的管理员会被坑：要么假装保存成功，要么收到指向新 DN 的报错；真实改名入口是右键 Modify DN。
- 建议：LDIF 模式锁定 DN 行（或解析后对比原 DN，发现变化时行内提示「改名请使用 Modify DN，本模式忽略 DN 变更」）；至少 diff 为空时不要静默关闭（区分"没有修改"与"修改被丢弃"）。

**P2-14 表单模式重复属性行静默丢值**

> R4 修复标注（2026-09-06）：已修复——attrRowsToAttributes 同名属性多值合并去重，LDIF 预览同步。
- 位置：`ldapDiff.ts` `attrRowsToAttributes()`（`result[name] = values` 后写覆盖）
- 复现：编辑态/新增态添加两行同名属性（如两行 `mailx` 分别填 `dup-a@x`/`dup-b@x`）→ 保存成功（"条目已保存"）→ 实际只存了 `dup-b@x`；LDIF 预览在保存前就已无声丢掉第一行。
- 影响：多值属性分多行输入是编辑器的自然用法，同名合并本应合成多值而不是覆盖；静默丢用户输入的数据。
- 建议：同名行合并 values（去重）而不是覆盖；或在保存前对重复属性名给行内警告。

**P2-15 搜索结果"恰好等于 sizeLimit"时无法区分"截断"与"巧合"（默认 500 即常见踩中值）**

> R4 修复标注（2026-09-06）：已修复——count===sizeLimit 且 truncated=false 时结果区显示「已到上限」徽标（title 含上限值，result.atLimitBadge 七语）。单测 + 浏览器复验通过。
- 位置：`SearchForm.vue`（sizeLimit 默认 "500"）+ `ResultTable.vue`（`truncated` 徽标只看后端标记）
- 复现：夹具 1008 条、sizeLimit=500 搜索 → 结果区显示「500 条 / 1 / 21 页」，无任何截断信号。真机后端若以截断+truncated=true 返回则有徽标；但恰好返回 500 整数上限时，用户无法分辨"目录恰好 500 条"与"被 500 截断"（P1-2 同族问题在搜索路径的变体）。
- 建议：`count === sizeLimit` 时给一次性行内提示「结果数达到条数上限，可能未完整」，引导调大 sizeLimit 或收窄过滤器。

**P2-16 sizeLimit / pageSize 数值字段无输入校验反馈**

> R4 修复标注（2026-09-06）：已修复——非法输入行内红字（search.invalidNumber 七语），留空或 0 = 不限制并在 label title 说明。单测 + 浏览器复验通过。
- 复现：sizeLimit 填 `abc`、`0`、`-5` → 全部静默按"无限制"发出（`positiveInt` 解析失败即 undefined），无行内红字、无 placeholder 说明；`0` 与清空行为一致但用户无从得知。
- 建议：非法输入给行内提示；或 label title 说明「0 或留空 = 不限制」。

**P2-17 全部 5 个弹窗无 `role="dialog"` / `aria-modal`（与 kafka 家族不一致）**

> R4 修复标注（2026-09-06）：已修复——5 个弹窗容器全部补 role=dialog + aria-modal=true + aria-label（各弹窗标题键）。浏览器复验通过。
- 位置：EntryEditorDialog / DeleteEntryDialog / ModifyDnDialog / SchemaPanel / ConnectionsPanel
- 实测：`[role="dialog"]` 计数 0（打开态逐一核查）；kafka 的 BrokersPanel/AclsPanel/GroupsPanel 均为 `role="dialog" aria-modal="true"`。
- 影响：读屏器无法识别对话框边界与背景惰性（焦点陷阱已做，但语义缺失）；违反工作区"公共交互对齐"惯例。
- 建议：`.modal` 容器补 `role="dialog" aria-modal="true" :aria-label`（title 文案现成）。

**P2-18 树与结果表 ARIA 结构缺失**

> R4 修复标注（2026-09-06）：已修复——树容器 role=tree、节点 role=treeitem + aria-level + aria-selected/aria-expanded；表头 aria-sort（前序完成）。浏览器复验通过。
- 实测：树容器无 `role="tree"`、节点无 `role="treeitem"`/`aria-level`（twisty 有 `aria-expanded`，P2-10 已补键盘）；结果表可排序表头无 `aria-sort`、容器无 `role="table"/"grid"`（表头 button + 行 button 的结构读屏器无法还原为表格）。
- 建议：树容器/节点补 tree/treeitem/aria-level（展开态已有 aria-expanded 可复用）；表头补 `aria-sort="ascending|descending|none"`；结果表容器给 `role="grid"` 级别语义或改用语义化表格结构。

**P2-19 树节点 button 嵌套 button（不合法 HTML）**

> R4 修复标注（2026-09-06）：已修复——twisty 与截断徽标降为 span[role=button]（twisty 附 aria-label 展开/收起并支持 Enter/Space），button 嵌套清零；CSS 选择器同步。TreeBranch.spec 更新 + 浏览器复验通过。
- 位置：`TreeBranch.vue`（`<button class="tree-node">` 内嵌 `<button class="tree-twist">` 与截断徽标 `<button class="tree-badge--truncated">`）
- 实测：DOM API（Vue）创建所以浏览器容忍、交互正常（`button-in-button` 断言 true），但 HTML 规范 button 不可包含交互元素；对读屏器与未来更严格的浏览器解析是隐患。
- 建议：tree-twist/徽标改为 `span role="button" tabindex` 或把外层改为 div + 内部两个按钮（P2-10 的 ↑/↓ 焦点逻辑同步调整选择器）。

**P2-20 ModifyDnDialog 新 RDN 无客户端预检（与 P2-6 收口的新增态不一致）**

> R4 修复标注（2026-09-06）：已修复——复用 isLikelyRdn，非法 RDN 行内红字 + 确认禁用（editor.rdnInvalid 七语复用）。单测 2 例 + 浏览器复验通过。
- 复现：右键重命名 → RDN 填 `cn=bad,dn`（或空段/缺 `=`）→ 确认可点 → 等服务器报 invalid DN。新增条目路径（P2-6）已用 `dn.ts isLikelyRdn` 做同样预检并禁用保存。
- 建议：ModifyDnDialog 复用 `isLikelyRdn`，行内红字 + 禁用确认，文案复用 `editor.rdnInvalid`（已有七语）。

**P2-21 搜索预设删除无确认**

> R4 修复标注（2026-09-06）：已修复——删除前确认弹窗（search.presetRemoveConfirm 七语，含预设名）。
- 复现：选中预设后点 trash 图标即刻删除（`removePreset`），无确认、无撤销。预设是持久化数据（sidecar 存储），误删成本虽低但不可恢复。
- 建议：加一次确认或 3 秒"已删除·撤销"通知（参照审计面板清空的轻量交互）。

### 已复核无问题维度（零发现）

| 维度 | 测了什么 | 结论 |
| --- | --- | --- |
| 性能与压力 | 展开 ou=people（500 子条目懒加载）59ms；折叠+重展开（状态保留）174ms；虚拟列表 1000+ 行仅挂载 36 行 DOM（overscan 8）；底部快速跳跃滚动 545ms 15 次无空洞/错位；全量搜索连续 5 次 28–34ms/次；heap 30.9→32.0MB（无泄漏迹象）；结果表 50 行/页固定挂载 | 无问题；交互延迟体感均 <100ms |
| 慢响应竞态 | 搜索在途：表单/按钮/表格全禁用，连点被 `searching` 守卫（无晚到响应覆盖）；树内过滤 seq 守卫在「在途时清除」下正确丢弃晚到结果（注入 900ms 延迟实测，清除后列表还原树视图）；树节点 loading 去重、loadMore 去重 | 无问题 |
| 保存失败恢复 | 注入 `ldap/entry/modify` 一次性失败：弹窗保持打开、修改值保留、saving 复位按钮解锁、重试成功关闭 | 无问题（恢复路径完整） |
| Esc/Enter 防重 | 编辑器 dirty 时 Esc 连按均否决、✕/取消显式关闭正常；ModifyDn 确认键连点被 `submitting` 守卫，仅提交一次 | 无问题 |
| 病态输入 | 20000 层嵌套过滤器粘贴：无栈溢出、无 page error，行内"过滤器不合法"+ 搜索禁用 | 无问题（validateLDAPFilter 递归深度在 20k 下存活） |
| 过滤器语义 | `(uid=user0001)` 精确 / `(cn=User 1*)` 前缀通配 111 条 / `(&(objectClass=person)(!(uid=user0001)))` 嵌套否定 999 条 / 非法括号组合正确禁用 / 构建器值 `a*b(c)\d` 正确转义为 `\2a\28\29\5c` / `(!(uid=x))` 与 `(!(&(uid=a)(cn=b)))` 源码↔构建器往返无损（notEquals 折叠正确）；`(cn=)` 空值断言（RFC 4515 合法）与 extensible `(uid:dn:=x)` 被宽松接受（可接受的设计取舍） | 无问题 |
| LDIF 往返 | 表单→LDIF→表单行数/值保持（6 行属性往返一致）；needsBase64 折行/续行在长值下经 unit 覆盖；LDIF 值修改的 dirty Esc 否决正常 | 往返一致（DN 行问题见 P2-13） |
| 分页/排序/键盘跨页 | 1008 条 21 页；cn 列排序（空值排首，localeCompare 语义正常）；↑/↓ 连按 51 次自动翻到第 2 页、Enter 打开对应条目 | 无问题 |
| 列宽拖拽持久化 | 拖拽生效（108px）、localStorage `ldap.result.columnWidths.v1` 写入 | 无问题 |
| schema 展示 | objectClasses 列 MUST/MAY 分行展示（ja「必須（MUST）」等术语正确）；MUST 必填属性在编辑器侧无强制提示（属于增强项，非缺陷——夹具 schema 无 MAY 反查入口，记为可选增强） | 基本无问题 |
| i18n 七语 | 脚本核查：七语各 177 key 完全对齐、0 缺失 0 多余、0 空值；en/es/it/pt-BR 无 CJK 泄漏；ja 171 个值确为日语。术语抽查 20 点：entry/條目・エントリ、截断/打ち切り・截斷、MUST/必填/必須、RDN 校验文案、审计/監査ログ/稽核紀錄 等均符合本地惯例 | 无问题 |
| 回归矩阵（第 2 轮收口保持） | `?theme=light` 工具栏 rgba(139,92,246,0.05)（P2-9 ✓）；`?ro=1` 只读徽标 + 菜单写项禁用 + 编辑器只读提示无保存按钮（✓）；`?noconn=1` spinner 停转 + identity 占位（P2-2 ✓）；`?locale=ja/zh-TW/en` 渲染正常 | 无回退 |

### 观察项（记录备查，不计缺陷）

- **删除非叶节点**：夹具服务器放行删除 `ou=services`，其子条目 `cn=ldap`/`cn=web` 残留在目录中成为"幽灵"（真机 OpenLDAP 会拒绝 non-leaf delete，code 66；AD 需逐级删）。UI 删除确认框无"该节点含子条目"提示——真机受服务器保护，建议后续在已加载子条目时提示一句。
- **App.vue `busy` ref 死代码**：声明并在 `runSearch` guard 中读取，但全文件无赋值 true 的路径（实际由 `searching` 承担），可清理。
- **SchemaPanel `definition: ""` 死字段**：`SchemaClassRow.definition` 恒为空串，`title` 悬停永远空白，可清理或补 raw definition 展示。
- **树 Tab 仍为每节点 2 个停止位**（node + twisty）：P2-10 已用 ↑/↓ 收口虚拟滚动可达性，Tab 序未改（与第 1/2 轮记录一致，非新发现）。
- AuditFeed denied/ok 事件在 mock 中仍不可达（ro 下 UI 预禁用全部写入口；ok 事件夹具不产生），与第 1 轮结论一致，需真机验证。
- 夹具 `ldap/search` 对 `sizeLimit` 到顶返回 `truncated: false`（与真实服务器语义不同），本轮 P2-15 的浏览器证据基于此行为；真机表现需复验。

### 统计

| 级别 | 本轮新增 | 累计（三轮） |
| --- | --- | --- |
| P0 | 0 | 0 |
| P1 | 1（P1-3 导出静默截断） | 3 |
| P2 | 9（P2-13 ～ P2-21） | 21 |
| 观察项 | 6 | 9 |

验证方式备注：本轮全部证据为 playwright 断言输出与 i18n 脚本比对，无截图产物；dev server 与 Chrome 已关闭。

## 七、第 5 轮（复核扫描，2026-09-06）

> 收敛判定轮：复核第 4 轮（R4）修复标注的 10 条是否真修好、修复是否引入新问题；
> 并按此前未覆盖的角度找新问题（连接切换状态隔离 / LDIF↔表单快速切换 / 大过滤器 /
> 末页翻页复位 / 删除与 ModifyDN 联动 / 只读弹窗矩阵）。只记录，不改代码。
> 本轮全程 0 未捕获 page error、0 console error。

### 环境表

| 项 | 值 |
| --- | --- |
| 轮次 | 第 5 轮复核扫描（2026-09-06，工作区无并行改动；R4 修复为未提交工作区改动） |
| Dev server | 本轮新起 `vite --port 5292 --strictPort`（ldap/frontend，结束已 kill），未跑 build |
| 自动化 | playwright-core 1.63 + 系统 Chrome（headless，channel=chrome），装于 `/tmp/uiscan-ldap-r5`（未进项目依赖） |
| 断言规模 | Part A（修复复核）45 通过 / 2 失败；Part B（新角度）59 通过；单测基线 `pnpm test` 23 文件 291 用例全绿 |
| 证据 | 全部为断言输出（未截图）；导出文件下载经 playwright download 事件捕获即弃 |

### 修复复核结论表（R4 标注 10 条）

| 项 | 复核结论 | 浏览器证据（断言摘要） |
| --- | --- | --- |
| P1-3 导出截断告知 | ✅ 真修好 | 右键导出 ou=people → LDIF 实收 1001 行 `dn:`（含 user0999，不再静默 500）；注入 `truncated=true` 后通知显示「子树导出已截断：仅导出前 N 条（达到 5000 条导出上限）」 |
| P2-13 LDIF dn 锁定+无变更通知 | ✅ 语义真修好（附 N2 时机滞后） | 仅改 dn 保存 → 通知「没有需要保存的修改」（不静默关闭）；改 dn+改属性 → 属性按原 DN 保存成功、未创建 ghost 条目；锁定提示存在但出现时机滞后（见 N2） |
| P2-14 多值合并 | ✅ 真修好 | 两行同名 `mailx` → LDIF 预览两行齐全 → 保存后服务器端 `["dup-a@x","dup-b@x"]` 双值都在；同名同值去重为 1 |
| P2-15 atLimit 徽标 | ✅ 真修好 | sizeLimit=500 命中 500 条 → 「已到上限」徽标，title 含上限值与建议文案；count<sizeLimit 时无徽标；count===sizeLimit（50/50）时徽标按设计出现 |
| P2-16 数值校验 | ✅ 真修好 | `abc`/`-5` 行内红字「请输入正整数（留空或 0 = 不限制）」；`0`/清空不报错；label title 说明「留空或 0 = 不限制」 |
| P2-17 role=dialog | ✅ 真修好 | 5 个弹窗（编辑器/删除/ModifyDN/Schema/连接）逐一打开：`[role=dialog][aria-modal=true]` 各 1，aria-label 均为标题文案 |
| P2-18 树/表 ARIA | ⚠️ **部分修复**（树 ✅ / 结果表 ✗，见 N1） | 树容器 role=tree、节点 treeitem+aria-level（1/2/3 层正确）；但结果表 `[aria-sort]` 计数 0——`ResultTable.ariaSortFor()` 是死代码，模板未接线（R4 标注「表头 aria-sort（前序完成）」与实际不符） |
| P2-19 无嵌套 button | ✅ 真修好 | `.tree-node` 内 button 后代计数 0；twisty 为 `span[role=button][tabindex=-1]` 带 Enter/Space 处理 |
| P2-20 RDN 预检 | ✅ 真修好 | ModifyDN 填 `cn=bad,dn` → 行内红字「RDN 无效…」+ 确认禁用；改回合法即恢复 |
| P2-21 预设删除确认 | ✅ 真修好 | 删除触发原生 confirm 且文案含预设名「确定删除搜索预设「R5-复核预设」？此操作不可撤销。」；取消保留、接受删除+「预设已删除」通知；未阻断正常流（单次确认） |

**修复引入新问题排查（均通过，未发现回归）**：

- 树 span 改造（P2-19）未破坏 P2-10 键盘导航：↑/↓ 焦点移动、Enter 选中、twisty Space 展开/收起均正常（A8c～A8f）。
- 树截断徽标（P1-2 回归）：「500+」→ 点击续载 → 精确「1000」，正常（A8g/A8h）。
- 预设删除确认（P2-21）未阻断批量流：确认/取消两条路径均按预期，无重复弹层。
- 编辑器遮罩 dirty 否决（P1-1 家族）：dirty 提示、Esc 否决在复验中未回退。

### 新发现清单

统计：**P0 × 0，P1 × 0，P2 × 4**。主旅程无回退，未达零新发现收敛（但全部为 P2 打磨项）。

**P2-22（N1）P2-18 的结果表 aria-sort 未接线——`ariaSortFor()` 是死代码，R4 修复标注部分不实**

> R6 修复标注（2026-09-06）：已修复——ariaSortFor 接入表头按钮 :aria-sort（dn 列 + 动态列），新增接线断言单测；浏览器复验 aria-sort 计数 > 0、dn 列 ascending。
- 位置：`ResultTable.vue`（L122-127 定义 `ariaSortFor()`，模板 header button 未绑定 `:aria-sort`；无 `role="grid"/"columnheader"` 语义）
- 复现：搜索后点击任一表头排序 → `document.querySelectorAll('[aria-sort]').length === 0`（树侧 role=tree/treeitem/aria-level 均在）。
- 影响：读屏器无法从表头得知当前排序列与方向；P2-18 只完成了树的一半，且单测/复验均未覆盖结果表半边（ResultTable.spec 无 aria 断言）。
- 建议：header button 补 `:aria-sort="ariaSortFor(column)"`（或挂到表头容器）；顺手补 ResultTable.spec aria 断言，避免再次出现「标注已修、实际未接线」。

**P2-23（N2）LDIF 的 dn 锁定提示不在 LDIF 模式内实时出现（P2-13 提示时机滞后）**

> R6 修复标注（2026-09-06）：已修复——watch(ldifText, ldifMode) 在 LDIF 模式内实时比对 dn，编辑即出锁定提示（无需切回表单）；浏览器复验通过。
- 位置：`EntryEditorDialog.vue`（`ldifDnChanged` 仅在 `syncRowsFromLdif()` 内更新，而该函数只在 `switchToForm()`/`save()` 被调用；LDIF 编辑过程无 `watch(ldifText)`）
- 复现：编辑态切 LDIF → 把 `dn:` 改成别的 DN → **LDIF 模式内无任何提示**；切回「表单」后才显示「LDIF 中的 dn 行不能用于重命名条目…」。若用户在 LDIF 内直接保存，提示从未展示（弹窗已关闭）。
- 影响：P2-13 的保存语义（dn 忽略、按原 DN 保存）已正确，但提示出现在用户视线已离开的地方；在 LDIF 内改 dn 的用户全程得不到反馈。
- 建议：加 `watch(ldifText)`（防抖）在 LDIF 模式内实时解析比对 dn 并显示锁定提示。

**P2-24（N3）连接切换（onContextChange）后工作台状态不重置：结果表/树/编辑器跨连接残留**

> R6 修复标注（2026-09-06）：已修复——syncConnectionContext 检测 connectionId 变化即重置结果表/搜索态并关闭三个写弹窗（树随 baseDn watch 自动重载）。mock 单连接无法触发真实切换，机制经代码路径验证，真机复验留待。
- 位置：`App.vue`（`onContextChange` 处理仅更新 `hostContext` 并 `syncConnectionContext()` 同步 identity/baseDn；不清理 results/editorEntry，不触发树刷新）
- 复现（模拟宿主下发新 context）：连接 A 展开 ou=people、选中节点、执行搜索（500 条）→ 切换 connectionId 到连接 B（同 baseDn）：工具栏 identity 正确变为新连接，但**树保持旧服务器数据（节点数不变）、结果表 500 条残留**；打开编辑器后再切到连接 C（不同 baseDn=ou=groups）：树正确重载到新根，但**结果表仍残留连接 A 数据、已打开的编辑器仍显示连接 A 的条目 uid=user0001**，且保存按钮可用——写操作将发往新连接（`setLdapConnectionId` 已切）。
- 影响：多连接环境下最危险的是编辑器残留：管理员以为在改 A 服务器的条目，实际 modify 发往 B 服务器（DN 撞上同名条目即静默改错数据）。结果残留则制造「entry not found」困惑。
- 夹具限制备注：mock 仅单连接，本轮经 init 脚本包装 `onContextChange` 注入新 context 模拟（不改业务代码）；真机多连接表现需复验。
- 建议：`syncConnectionContext` 检测 `connectionId` 变化时：关闭打开的编辑器/写弹窗、清空 results/hasSearched、强制 `treeRef.invalidate()`（不依赖 baseDn 变化的 watch）。

**P2-25（N4）删除/ModifyDN 后树已刷新但结果表不联动，残留行点击报错（与 P2-24 同族的单连接版本）**

> R6 修复标注（2026-09-06）：已修复——删除/ModifyDN 后若受影响 DN 仍在当前结果中则重放最近一次搜索；浏览器实测删除后原条目从结果中消失（重搜补足下一批属预期）。
- 位置：`App.vue`（`confirmDelete`/`confirmRename`/`onEditorSaved` 只做 `treeRef.invalidate(...)`，无结果表失效逻辑）
- 复现：搜索全量 → 删除 `cn=web`（树正确消失）→ 结果表仍显示 `cn=web` 行 → 双击该行 → 错误横幅 `entry not found: cn=web,ou=services,…`；重命名 `uid=user0006` 后同样残留旧 DN 行。
- 影响：写后结果区展示已不存在的条目；行数徽标也不准。用户困惑「明明删了还在」。
- 建议：写操作成功后按当前过滤器重跑一次搜索（或至少从 results 中移除/改名对应行并更新 count）；与 P2-24 的状态重置一并收口。

### 零发现维度证据（本轮测过且无问题）

| 维度 | 测了什么 | 结论 |
| --- | --- | --- |
| LDIF↔表单快速切换 5 次 | 表单改值→LDIF→表单 ×5（每次断言双向一致）；LDIF 内改值→60ms 间隔快速连点来回 5 次；dirty 提示保持；保存持久化最终值 | 往返完全一致、快速连点不丢状态、保存正确 |
| 大过滤器（500+/2500+ 字符） | 633 字符 45 分支 OR：合法性、搜索命中 45 条、构建器解析 90 输入（345ms，可感知但可用）、预览等价、源码往返无损；2560 字符 180 分支：无栈溢出无报错、构建器可用 | 无问题 |
| 末页翻页后改过滤器 | 21 页→第 21 页→改过滤器 1 条：页码复位 1/1、计数正确；第 3 页→pageSize=100 重搜：复位第 1 页；末页→0 匹配：空态文案正确 | 页码复位正确（`entries` watch page=0） |
| ro=1 弹窗写入口禁用矩阵 | 右键菜单：新增/重命名/删除禁用，查看/在此搜索/导出子树/复制 DN 可用；编辑器：只读提示+无保存按钮+输入与 LDIF textarea 禁用+Esc 可直接关；删除/ModifyDN 弹窗经菜单不可达（点禁用项无弹层）；Schema/连接弹窗（读路径）可开 | 矩阵完整，无 ro 写入口泄漏 |
| 回归（修复间互扰） | 树键盘导航 × span 改造、截断徽标续载、预设确认流、遮罩/Esc 守卫 | 无回归 |
| 稳定性 | 6 个场景组全程（含连接切换注入、快速连点、2560 字符解析）0 pageerror、0 console error | 无问题 |

### 观察项（记录备查，不计缺陷）

- **pageSize 与结果表分页的语义差**：`分页大小` 是服务端 paged-search 聚合参数；结果表始终按固定 50 行/页客户端切片（`ResultTable.PAGE_SIZE`）。pageSize=100 时 1008 条仍显示「1 / 21」，用户可能预期 11 页。建议在 label title 或分页器旁说明。
- **ro 下预设保存/删除仍可用**：预设是插件侧客户端偏好（非目录写），不禁用合理；删除确认流在 ro 下同样正常（B6q 验证）。若产品倾向 ro 完全只读，可再议。
- **B3 构建器解析 45 条件耗时 ~345ms**：可感知（源码→构建器切换瞬间延迟），远低于可用性阈值，仅记录。
- 连接切换注入依赖 init 脚本包装 `onContextChange`（mock 单连接），P2-24 的真机多连接表现需在真实宿主复验后定级。

### 统计

| 级别 | 本轮新增 | 累计（五轮） |
| --- | --- | --- |
| P0 | 0 | 0 |
| P1 | 0 | 3 |
| P2 | 4（P2-22 ～ P2-25，含 1 条 R4 标注不实的部分修复） | 25 |
| 观察项 | 4 | 13 |

**收敛判定：未达零新发现**（4 条 P2），但 R4 十条修复中 9 条完整通过、1 条部分修复（P2-18 结果表半边），主旅程与全部回归矩阵无回退；新发现均为打磨项，无阻断与数据丢失级问题。第 6 轮建议只针对 P2-22 ～ P2-25 修复后做点验，可收敛。
