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
