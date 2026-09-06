# M6-LDAP 路进度记录（phpLDAPadmin 对标追赶：N1–N4 并发实施）

> 记录人：M6-LDAP（worktree feat/ldap-m6，并发 agent 实施）。
> 日期：2026-09-06。工具链：go 1.27.0 homebrew（darwin/arm64）/ node 22.21.0 /
> pnpm 11.24 / Docker 29.4.0（OrbStack）/ dbx-plugin CLI 0.1.0。
> 对标依据：`docs/PLA_GAP_ANALYSIS.zh-CN.md` §7（2026-09-06 定稿）；路线归
> IMPL_PLAN §9 M6（扩容后）。
> 凭据红线：容器管理员密码随机生成、仅经环境变量（compose `LDAP_ADMIN_PASSWORD`
> → smoke `LDAP_TEST_BINDPW`），未写入任何文件/日志/报告；S12 断言审计与事件
> 不含密码明文/哈希；前端密码编辑器明文仅一次性通知、不落组件状态外存储。

## 0. 组织方式

- 工作区：`git worktree /Users/Jinpy/btroot/wt-ldap-m6`（分支 `feat/ldap-m6`，
  自 master 7b39460）；主工作区 ldap/ 未提交文档（规划三件套 + manifest
  0.1.42 + UI_SCAN 第 6 轮）已同步进 worktree，主工作区其余脏文件未触碰。
- 并发分工（文件域互斥，共享文件 `lib/i18n.ts`/`lib/api.ts`/
  `EntryEditorDialog.vue` 由主控收口）：
  - Agent A：N1 sidecar（`recursive` 删除 + `childrenCount`）+ 基线修复 +
    smoke S12–S14；
  - Agent B：N2 `lib/passwordHash.ts` + `PasswordAttributeEditor`（独立组件）；
  - Agent C：N3 `lib/binaryValue.ts` + `BinaryValueEditor`（独立组件）；
  - Agent D：N4 `lib/newEntryTemplates.ts` + `NewEntryWizard`（独立组件）。
- 前端 agents 约束不碰 i18n/集成点，i18n key + 七语文案由主控合并（53 key
  × 7 语 + plainNotice/recursive 两个收口期新增 key）。

## 1. 完成项

### 1.1 N1 子树删除（完整闭环）

- sidecar：`ldap/entry/delete` 增 `recursive?`（Tree Delete 控件
  `1.2.840.113556.1.4.805` 优先，服务端不支持回退自底向上逐层删除，上限
  1000 条）；新增 `ldap/entry/childrenCount`（scope=one，上限 5000，
  风格同 `ldap/count`）；递归删除审计聚合一条（`subtree_delete` +
  `deletedCount`）。IMPL_PLAN §5.2 契约表已同步。
- 前端：DeleteEntryDialog 打开时 best-effort 拉取 childrenCount（无桥/旧
  sidecar 静默降级单条语义），有子条目时展示计数 + 递归勾选；App.vue
  `confirmDelete(options)` 透传 recursive。
- 测试：后端 8 个新单测（`subtree_test.go`）；DeleteEntryDialog.spec 3 个
  新用例（递归勾选/叶子隐藏/降级）。

### 1.2 N2 密码哈希辅助

- `lib/passwordHash.ts`：Web Crypto 实现 {SSHA}/{SSHA256}/{SSHA512}/{SHA}/
  {SHA256}/{SHA512}/{CLEARTEXT}（RFC 2307 形态，盐拼 digest 输入侧）+
  parse/generateRandomPassword/verifyPasswordHash；MD5/crypt/argon2/bcrypt
  类型层排除（对账表 §6 不追赶）。
- `PasswordAttributeEditor`：scheme 下拉（缺省 {SSHA}）+ 随机生成 +
  哈希写入 + 已有值 scheme 识别；EntryEditorDialog 按 `*password*`/unicodePwd
  属性名分流接入；随机明文仅一次性 notify（新 key
  `ldap.passwordEditor.plainNotice`），不进 rows/LDIF/审计。
- 期间修复真实 bug：初版盐拼在 digest 输出侧，被 node:crypto 双向对照用例
  捕获后修正。
- 测试：25 单测/组件用例。

### 1.3 N3 二进制查看 + 上传

- `lib/binaryValue.ts`：魔数嗅探（jpeg/png/gif/webp/pem）、base64↔bytes
  分块转换、hexView 三列、prettyPem、5MB 上限、`looksBinaryAttribute`
  启发式（schema OID 缺失时兜底）。
- `BinaryValueEditor`：多值卡片（预览/hex/PEM 三视图 + 删除值 + 多文件
  上传 5MB 前端拦截）；EntryEditorDialog 按 `looksBinaryAttribute` 分流接入。
- 测试：37 单测/组件用例；smoke S14 jpegPhoto 往返 PASS。

### 1.4 N4 模板化新建 + objectClass 补加引导

- `lib/newEntryTemplates.ts`：4 内置模板（user/group/ou/simpleObject）+
  空白；must/may 聚合（schemaCache SUP 父类链优先，RFC 常识链兜底）；
  `buildDn` RFC 4514 最小转义。
- `NewEntryWizard`：模板 → objectClass 增删（must/may 即时重算，即"补加
  引导"语义）→ DN → must 表单四步；App.vue 接管树「新增子条目」入口
  （原 EntryEditorDialog add 态保留未删）。
- 测试：22 单测/组件用例。

## 2. 顺带修复（基线既有问题）

1. **manifest 契约矩阵测试过期**（HEAD 既有失败）：M5-a 给 tls_verify/
   tls_ca_path/tls_server_name 加了 `tls_mode ∈ {starttls,ldaps}` 联动显隐，
   `TestManifestAuthTypeVisibilityMatrix` 的恒可见 common 列表未同步。已把
   三字段移出 common 并补 tls_mode 条件场景（不改 manifest）。
2. **blocked_attributes 覆盖语义**：`firstBlockedLDAPAttribute` 原无条件
   并集内置默认表，配置永远无法移除 userPassword，与 IMPL_PLAN §6.2
   「blocked_attributes 覆盖默认表」相悖。改为显式配置覆盖、空配置兜底
   默认表；2 个策略单测同步（N2 密码写入的前置）。
3. **UI 走查 backdrop 检查**（HEAD 既有失败，主工作区基线同样 6/7）：
   独立走查环境无宿主主题 CSS，`--overlay` 未定义 → 遮罩全透明；且检查
   的 alpha 正则只兼容 modern color() 语法（注释声称兼容 legacy rgba），
   阈值 0.9 亦高于主题设计值（暗色遮罩 = background 70% mix，themeSync）。
   修复：style.css `:root` 补 `--overlay: color-mix(background 70%,
   transparent)` 兜底（宿主带属性选择器优先级更高，真机不受影响）+
   ui_test.mjs alpha 解析改取尾随数字、阈值对齐设计值 0.7。走查 7/7。

## 3. 验证数字（收口态，全部在 worktree 实跑）

- 前端：typecheck 0 错误；vitest **29 文件 / 381 用例全绿**（基线 292 无回归，
  新增 89）；`pnpm build` ✓。
- UI 走查（scripts/ui_test.mjs，headless Chrome）：**7/7**。
- 后端：`go vet ./...` 0 错误；`go test ./...` **4 包全 ok**（含 N1 新增
  8 用例）。
- 打包：`io.dbx.ldap-0.1.42-darwin-arm64.dbxp`（5.96MB）✓（见 §4 注记）。
- smoke（OpenLDAP 容器 1389 实跑）：**15 场景 15 PASS / 0 FAIL / 0 SKIP**，
  含 S12（密码哈希写入 + bind 验证 + 审计脱敏）、S13（childrenCount +
  递归删除 + 聚合审计；容器无 Tree Delete 控件，实际验证回退路径）、
  S14（jpegPhoto 往返）。

## 4. 遗留与备注

- **dbx-plugin CLI 0.1.0 打包 workaround**：CLI 在 staging 目录生成 go 1.22
  workfile，与 backend go 1.24.0 模块冲突（PROGRESS-P 时代 /tmp/go-shim 的
  同款问题，tmp 已清理）。本轮 shim 为强制 `GOWORK=off exec go`（CLI 经
  PATH 调 go，已验证拦截生效）；shim 未入库，后续可考虑升级 CLI 或
  test.sh 内建处理。
- **M6 剩余**：LDIF 导入、结果批量操作（ADS 对账表原 M6 项）本轮未做；
  前端"校验密码"入口（bind 语义，smoke 已验协议路径）P2；编辑态补
  objectClass 引导 P2。
- smoke 场景编号：规划期 S11–S14 因 S11 已被 rootDse namingContexts 场景
  占用顺延为 S12–S14（PLA 对账表 §7 已注明）。
- 测试容器 `dbx-ldap-test`（1389）保留运行；全部改动**未 commit**，留在
  worktree feat/ldap-m6 待审。
