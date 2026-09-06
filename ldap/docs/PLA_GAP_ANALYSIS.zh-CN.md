# LDAP 插件 vs phpLDAPadmin（PLA）差距分析

> 对标基准：`leenooks/phpLDAPadmin`（v2 master，含 v2.2 JSON 模板引擎；
> 核查日期 2026-09-06。v1.2.x 官方已废弃且存在未修复漏洞，不再对标）。
> 证据面：PLA `routes/web.php`（功能面路由）、`app/Classes/LDAP/Attribute/*`
> （属性类型类树）、`templates/*.json`、`config/pla.php`。
> 状态标记：✅ 已有 ｜ 🔶 部分（有差距）｜ ❌ 缺失。
> 优先级：P0 = 下一轮就做；P1 = 近期；P2 = 远期/按需。
> 与 ADS 对账表关系：**并轨**——追赶路线沿用 `ADS_GAP_ANALYSIS.zh-CN.md`
> §11 的 M5/M6/M7 框架，本文件是 **PLA 侧唯一对账表**，落地一项更新一项。

## 1. 结论摘要

- **协议/连接/认证/安全面：本插件全面超越 PLA**（PLA 仅 anonymous/simple
  bind，README 明确声明无安全实现）。
- **差距集中在「数据生产效率」**：模板化新建、密码哈希辅助、二进制属性、
  LDIF 导入、条目复制、子树删除——PLA 作为"管理员日常改数据"工具的核心
  效率特性，本插件多为空白或仅排期了弱化版。
- 新增实施项 N1–N4（§7），其中 **N2 密码哈希辅助建议提级**（现状
  `userPassword` 原值写入，写错哈希格式会直接把账号写坏）；**N1 子树删除**
  是 ADS 对账表漏项，本文件收编。
- PLA v2 自身未完成项（binary upload、jpegPhoto create/delete、objectClass
  移除清理属性、group membership、attr tags、属性唯一性）不必视为成熟基线，
  本插件可一次做到位。

## 2. 连接 / 认证 / 协议（超越为主）

| 能力 | PLA | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 认证方式 | ✅ anonymous + simple（login_attr=uid 时服务端搜索定位 DN） | ✅ 8 种（Kerberos password/keytab/ccache、NTLM/NTLM hash、DIGEST-MD5、SASL EXTERNAL…） | ✅ 超越 | — |
| 简单 bind 按 uid 定位 DN | ✅（uid + objectclass 过滤搜索） | ❌（DN 直连；Kerberos 等场景由 realm/SPN 覆盖） | 🔶 | P2：simple bind 加 `loginAttr` 搜索定位（复用现有 search，无新协议方法） |
| LDAPS / StartTLS | ✅ | ✅ tls_mode=ldaps/starttls + tls_verify/ca_path/server_name | ✅ 超越 | — |
| ldapi://（Unix socket） | ❌ | ✅ | ✅ 超越 | — |
| 匿名浏览（登录前） | ✅ allow_guest | ✅ anonymous 认证 | ✅ | — |
| base DN 覆盖 | ✅（rootDSE namingContexts 或配置覆盖） | ✅（rootDSE 派生 + profile.base_dn） | ✅ | — |

## 3. 数据生产与条目编辑（核心差距）

| 能力 | PLA | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 模板化新建条目 | ✅ 核心卖点：JSON 模板（user_account/ou/o/mail_account/dns_domain）+ `templates/custom` 目录 + force_may | 🔶 空白新增（objectClass 手填） | 🔶 | P1：N4（§7）objectClass must 模板 + 内置常用模板，不做完整模板引擎 |
| uid/gid 自动编号 | ✅ template.getnextnumber（uidnumber/gidnumber 起始值） | ❌ | ❌ | P2：并入 N4 模板变量 `{autoNumber}`（OpenLDAP 无 DNA 插件时常用） |
| 密码哈希编码 | ✅ 21 种（`Attribute/Password/*`：SSHA/SSHA256/512、argon2i/id、bcrypt、sha256crypt…） | ❌ 原值写入（后端无任何哈希实现，grep 证实） | ❌ | **P1：N2（§7）提级**——Web Crypto 覆盖 sha/ssha/sha256/ssha256/sha512/ssha512 + clear；argon2/bcrypt 等 PHP 特化为不追赶 |
| 密码校验 | ✅ `entry/password/check`（modal：userpassword-check） | ❌ | ❌ | P1：随 N2——用"DN + 明文 bind 验证"语义，**不用** Compare 扩展（§0 非目标） |
| 随机密码生成 | ✅（v1 随机生成） | ❌ | ❌ | P1：随 N2（前端生成，进剪贴板不入日志） |
| 二进制属性查看 | ✅ JpegPhoto/Certificate 显示（`Attribute/Binary/*`）+ user image 路由 | ❌（textarea 原样） | ❌ | P1：N3（§7），扩 ADS 已列"查看器" |
| 二进制属性上传 | 🔶 v2 README 列 outstanding（jpegPhoto create/delete 未完成） | ❌ | ❌ | P1：N3（§7）上传；PLA v2 自己也没做完，本插件一次做到位 |
| LDIF 导入 | ✅ `Import/LDIF.php` + `entry/import/process/{type}` | ❌ | ❌ | P0/P1：已在 ADS 对账表 M6 ✅ 路线 |
| LDIF 导出 | ✅ `Export/LDIF.php` | ✅（结果 + 子树，另有 CSV/JSON） | ✅ | — |
| 条目复制/移动 | ✅ `entry/copy-move`（跨 DN） | ❌ | ❌ | P2：已在 ADS 对账表 |
| 子树删除 | ✅ delete modal + `ajax/subordinates` 子条目计数确认（v1 递归删除） | ❌ 仅单条删除（后端 grep 无 tree-delete 控件/递归删除） | ❌ | **P1：N1（§7），ADS 对账表漏项，本文件收编** |
| objectClass 补加引导 | ✅ `entry/objectclass/add`（补 objectClass 带出 must 属性） | 🔶 手工编辑可实现，无 schema 引导 | 🔶 | P1：并入 N4（schemaCache must/may 已有） |
| 条目重命名/移动 | ✅ rename modal | ✅ modifyDn（newParentDn 支持） | ✅ | — |

## 4. 搜索 / Schema / 信息面

| 能力 | PLA | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 树浏览/懒展开 | ✅ ajax children/bases | ✅ 虚拟列表 + 计数徽章 + 关键字过滤 | ✅ | — |
| 搜索 | 🔶 单 filter 串（v2）/ simple+advanced 表单（v1） | ✅ 可视化条件构建器 + RFC4515 双向 + Paged Results 控件 | ✅ 超越 | — |
| RootDSE | ✅ server/info | ✅ | ✅ | — |
| Schema 浏览 | ✅ AttributeType/ObjectClass + **LDAPSyntax/MatchingRule/OID/SASL 机制**（`Schema/*` + ldap_supported_oids.txt） | 🔶 属性/objectClass + must/may | 🔶 | P2：语法/匹配规则原始定义透出（ADS 对账表已列） |
| 属性显示排序/友好名 | ✅ attr_display_order | ❌（字母序） | ❌ | P2：内置常用序（cn/givenName/sn/uid/mail…），不做用户配置 |
| datetime 格式化 | ✅ datetime_format | 🔶 部分内部属性 | 🔶 | P2：internal Timestamp 属性统一格式化 |

## 5. 安全与审计（超越项）

| 能力 | PLA | 本插件 |
| --- | --- | --- |
| DN 白名单（读/写分离） | ❌（README：无安全实现） | ✅ |
| 屏蔽属性（读过滤 + 写拒绝） | ❌ | ✅ |
| 只读门禁 | ❌ | ✅（表单 ∥ 宿主双层） |
| 写操作审计 | ❌ | ✅ audit feed 面板 + jsonl 落盘 |
| 可视化主题/七语 | 🔶 单主题 + 社区翻译 | ✅ 宿主主题跟随 + 七语 |

## 6. 明确不追赶项

| 项 | 理由 |
| --- | --- |
| argon2/bcrypt/extdes/md5crypt 等 PHP 侧哈希 | OpenLDAP 默认 scheme 覆盖已足够；JS 侧实现成本高、使用面窄 |
| FreeIPA 特化属性类型（KrbPrincipalKey/KrbTicketFlags） | PLA 生态特化，非通用 LDAP 能力 |
| `update/proxy` 代理更新、Docker 部署形态 | 与 DBX 宿主连接模型/插件分发形态不匹配 |
| attr tags、模板属性唯一性校验 | PLA v2 亦未完成；有屏蔽属性 + 审计兜底，按需再议 |
| LDAP_UPDATE_PROXY、allow_guest 独立开关 | anonymous 认证已等价覆盖 |

## 7. 实施规划（并入 M5–M7，任务编号 L6-x）

> M6 由 ADS 对账表 §11 原四项（LDIF 导入、结果批量操作、新条目
> objectClass 模板、二进制属性查看器）扩容为本节 N1–N4；路线归属以
> IMPL_PLAN §9 更新后的表述为准。落地一项更新本表状态 + IMPL_PLAN 契约。

### N1 子树删除（P1，L6-1）

- **现状**：`ldap/entry/delete` 仅单条；后端无 tree-delete 控件/递归实现。
- **目标**：前端删除确认列出子条目数（对齐 PLA `ajax/subordinates` 语义），
  支持递归删除。
- **契约草案**：`ldap/entry/delete` 增加可选 `recursive?: boolean`；
  新增 `ldap/entry/childrenCount`（`{dn}` → `{count, truncated?}`，scope=one，
  上限复用 `ldap/count` 的 5000）。方法落地时同步 IMPL_PLAN §5.2 契约表。
- **技术要点**：优先服务端 Tree Delete 控件 `1.2.840.113556.1.4.805`
  （go-ldap DelRequest 可挂 Controls；OpenLDAP 支持）；服务端不支持
  （返回 unavailable/unwillingToPerform）则回退**先序自底向上**逐层删除
  （sub 搜索按深度排序，上限 1000 条防误删）。两路径均受只读门禁 + 写
  白名单 + 审计约束（审计记一条聚合记录：action=subtree_delete，含条数）。
- **DoD**：单测（控件挂载/回退排序/上限）；smoke S12；前端确认弹窗 +
  七语；ADS/PLA 对账表状态更新。

### N2 密码哈希辅助（P1，L6-2，提级）

- **现状**：`userPassword` 原值写入，无哈希、无校验、无随机生成。写错
  哈希格式（如裸明文入 OpenLDAP）账号即失效。
- **目标**：EntryEditor 密码属性行升级为密码编辑器：scheme 下拉
  `{SSHA}` `{SSHA256}` `{SSHA512}` `{SHA}` `{SHA256}` `{SHA512}`
  `{CLEARTEXT}`（缺省 `{SSHA}`，随机盐）+ 随机密码生成 + 校验。
- **实现面**：纯前端 `lib/passwordHash.ts`（Web Crypto `digest` 全覆盖上列
  scheme，MD5/crypt 明确不支持并写入 §6 不追赶）；sidecar 零改动（值仍走
  entry/modify，normalizeLDAPWriteValues 与屏蔽属性拦截语义不变）。
- **安全约束**（硬性）：密码明文/哈希值**不进**审计事件、audit.jsonl、
  stderr 日志与 i18n 之外任何落盘；审计仅记 `action=password_modify`。
- **与屏蔽属性的关系**：`userPassword` 在默认屏蔽表内——管理员需显式从
  `blocked_attributes` 移除方可写密码（现状语义保留，不为此开旁路）；
  前端检测到被屏蔽时在编辑器内提示原因，不静默失败。
- **校验语义**：对目标 DN 用"输入密码 + simple bind 一次"验证成功/失败，
  复用现有 dial/bind 基建，不引入 Compare/passwd 扩展操作（守住 §0 非目标；
  RFC 3062 passwd extend 维持 ADS 对账表 P2 不变）。
- **DoD**：lib 单测（向量：RFC 2307 `userPassword` 格式往返）；组件测试；
  smoke S11；审计脱敏断言；七语。

### N3 二进制属性查看 + 上传（P1，L6-3，扩 ADS 已列"查看器"）

- **现状**：二进制值以 textarea 原样（base64）呈现。
- **目标**：EntryEditor 二进制语法属性（jpegPhoto `1.3.6.1.4.1.1466.115.121.1.28`、
  证书类等）支持：图片预览 / hex 查看 / 证书 PEM 美化（只读三视图）+ 文件
  上传（FileReader → base64 → entry/modify add/replace，无新协议方法）+ 删除值。
  对齐 PLA `Attribute/Binary/*`（PLA v2 上传未完成，本插件一次到位）。
- **技术要点**：schema 缓存按 syntax OID 识别二进制属性，schema 不可用时
  按属性名启发式（jpegPhoto/photo/*Certificate）；上传单值上限 5MB（前端
  拦截，防 stdio-jsonl 消息过大）；base64 读写往返现有二进制通道语义不变。
- **DoD**：组件测试（预览/上传/大小拦截）；smoke S13（种子数据加 jpegPhoto
  样例，读回 base64 一致）；浏览器走查入 ui_test.mjs；七语。

### N4 模板化新建 + objectClass 补加引导（P1，L6-4，扩 ADS 已列"模板"）

- **现状**：空白新增（objectClass 手填，无 must 引导）。
- **目标**：① 新建向导：选 objectClass（schema 缓存驱动）→ 自动铺 must
  属性 + 常用 may；内置 4 个轻模板（user / group / ou / simpleObject，
  对齐 PLA templates 精简版）；② 已有条目补 objectClass 时同样带出 must
  属性（对齐 PLA `entry/objectclass/add`）。**不做** PLA 完整 JSON 模板
  引擎与自定义模板目录。
- **技术要点**：模板 = 前端常量 + schemaCache must/may 派生，零 sidecar
  改动；uid/gid 自动编号（P2）留 `{autoNumber}` 变量位，本期不做。
- **DoD**：组件测试（must 铺开/校验）；浏览器走查；七语；对账表更新。

### Smoke 场景增补（S11–S14，入 scripts/smoke_test.py）

| # | 场景 | 断言 |
|---|---|---|
| S11 | 密码哈希写入（{SSHA}）→ DN+明文 bind 验证 → 审计无值泄漏 | bind 成功；audit 仅 password_modify 动作 |
| S12 | 父条目下挂 2 层子树 → childrenCount → recursive delete | 子 DN 全部消失；审计一条聚合 |
| S13 | jpegPhoto 上传（<5MB）→ entry/get 读回 | base64 一致 |
| S14 | 服务端不支持 tree-delete 控件的回退路径（容器禁用控件模拟） | 回退递归删除成功 |

### 归属总览

| 批次 | 内容 |
| --- | --- |
| M5-b（ADS 既有，不变） | NOT 组 UI、`ldap/check` 分级、搜索历史、LDIF 编辑生效 |
| M6（扩容后） | LDIF 导入、结果批量操作、**N4 模板化新建**、**N3 二进制查看+上传**、**N1 子树删除**、**N2 密码哈希辅助** |
| M7（按需） | mTLS、CRAM-MD5、referral、服务器端排序、digest realm、DSML、schema 语法/匹配规则透出、uid 定位 DN、uid/gid 自动编号、属性显示排序、datetime 统一格式化 |
