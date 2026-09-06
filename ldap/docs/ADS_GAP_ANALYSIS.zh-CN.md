# LDAP 插件 vs Apache Directory Studio（ADS）差距分析

> 对标基准：Apache Directory Studio 2.x（Connection Wizard / LDAP Browser /
> Schema Browser / Search / Import-Export / Batch Operations）。
> 状态标记：✅ 已有 ｜ 🔶 部分（有差距）｜ ❌ 缺失。
> 优先级：P0 = 下一轮就做；P1 = 近期；P2 = 远期/按需。
> 本文件是 **ADS 侧**追赶路线唯一对账表（PLA 对标见
> `PLA_GAP_ANALYSIS.zh-CN.md`，两表并轨同一 M5/M6/M7 框架，M6 扩容以
> IMPL_PLAN §9 更新后的表述为准）；落地一项更新一项（状态 + 落地记录链接）。

## 1. 连接方式与网络

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 明文 LDAP (ldap://) | ✅ | ✅ tls_mode=none | ✅ | — |
| LDAPS (ldaps://) | ✅ | ✅ tls_mode=ldaps（后端 ldaps/StartTLS 已绿 smoke） | ✅ | — |
| StartTLS | ✅ | ✅ tls_mode=starttls | ✅ | — |
| LDAPI (Unix socket) | ❌（ADS 无） | ✅ ldapi://（smoke_ldapi_test） | ✅ | 超越项，保持 |
| 连接测试（只测网络连通） | ✅ Check Network Parameter | 🔶 宿主连接表单有 test capability（整体 bind 测试）；无"仅网络/仅认证"分段测试 | ❌ | P1：sidecar `ldap/check`（network|bind 两级），连接表单已可挂 |
| 网络超时/读超时 | ✅ | 🔶 单一 timeout_secs（缺省 30） | 🔶 | P2：拆 dial/read 两档 |
| 引用（referral）跟随策略 | ✅ follow/ignore/manage | ❌ | ❌ | P1：go-ldap ReferralEnabled + 配置项 |
| 别名处理（browse/search 解引用） | ✅ 分开设置 | 🔶 搜索有 derefAliases，浏览树无 | 🔶 | P2 |

## 2. 加密与证书

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 证书校验开关 | ✅（truststore / 自定义 CA / 不校验） | ✅ tls_verify + tls_ca_path | ✅ | — |
| 自定义 CA 证书 | ✅ | ✅ tls_ca_path | ✅ | — |
| SNI/服务器名覆盖 | 🔶（跟随 URL） | ✅ tls_server_name | ✅ | — |
| 客户端证书（mTLS） | ✅ | ❌ | ❌ | P2：Go tls.ClientCerts + 表单字段 |
| 加密关闭时隐藏证书设置 | ✅ | 🔶 本轮补：manifest tls_* 字段随 tls_mode 联动显隐 | ✅（本轮） | — |

## 3. 认证

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| Anonymous / Simple | ✅ | ✅ | ✅ | — |
| Unauthenticated bind | 🔶（ADS 无独立项） | ✅ | ✅ | — |
| Kerberos/GSSAPI（纯 Go） | ✅（原生 KRB） | ✅ password/keytab/ccache、realm、KDC host/port、krb5.conf（smoke_auth 矩阵） | ✅ | — |
| Kerberos 原生票据缓存/kinit | ✅ | ✅ ccache 凭据类型 | ✅ | — |
| DIGEST-MD5（realm/QoP/authzid） | ✅ | 🔶 sasl_host 覆盖 + authzId；realm 未单独暴露（由服务端 challenge 驱动） | 🔶 | P2：digest realm 字段 |
| NTLM / NTLM hash | 🔶（ADS 经 SASL NTLM） | ✅ | ✅ | — |
| SASL EXTERNAL | ✅ | ✅ | ✅ | — |
| CRAM-MD5 | ✅ | ❌（tiny-rdm 也无） | ❌ | P2 |
| SASL QoP（auth/auth-int/auth-conf） | ✅ | ✅ sasl_qop（kerberos） | ✅ | — |
| GSSAPI mutual auth | ✅ | ✅ sasl_mutual_auth | ✅ | — |
| 授权身份（authzid / proxy） | ✅ | ✅ authzId 字段（后端） | ✅ | — |

## 4. 连接设置排版

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 分区/tab：Network → Authentication → Advanced | ✅ 四个 tab | 🔶 宿主表单单页平铺（字段顺序由 manifest 决定） | 🔶 | 本轮：字段重排（名称→网络→加密→证书→BaseDN→认证→Kerberos/SASL→高级→安全策略）+ TLS 字段联动显隐 |
| 字段二级联动 | ✅ | ✅ visible_when/required_when（auth_type、krb_credential_type、tls_mode） | ✅ | — |
| 连接颜色/分组 | ✅（folder） | 🔶 宿主侧连接能力 | 🔶 | 随宿主 |

## 5. 搜索与条件构建

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 可视化条件构建器（AND/OR 嵌套） | ✅ Filter Editor | ✅ FilterGroup 树 ≤2 层 + 实时预览串 | ✅ | — |
| NOT 组 / NOT 条件 | ✅ | 🔶 `(!(attr=value))` 解析回填为 ≠ 运算符（✅ 本轮）；NOT 组 UI 未暴露（解析保留 negate 语义） | 🔶 | P1：NOT 组开关 |
| 运算符集 | ✅ =/≠/≥/≤/≈/presence/子串 | ✅ equals/**notEquals（本轮）**/contains/startsWith/endsWith/present/gte/lte/approx | ✅ | — |
| 源码模式 + 双向解析 | ✅ | ✅ RFC4515 直编 + best-effort 解析回构建器 | ✅ | — |
| 值转义（RFC 4515） | ✅ | ✅ \5c/\2a/\28/\29/\00 + UTF-8 hex 串解码 | ✅ | — |
| schema 驱动属性下拉 | ✅ | ✅ datalist（schema 缓存 30min + 常用集兜底） | ✅ | — |
| 保存的搜索（Saved Searches） | ✅ 文件夹管理 | ✅ 预设（结构化条件 + 过滤器串，sidecar 持久化） | ✅ | P2：预设分组/排序 |
| 搜索历史 | ✅ | ❌ | ❌ | P1：本地最近 N 条过滤器历史下拉 |
| 分页搜索（Paged Results 控件） | ✅ | ✅ pageSize | ✅ | — |
| 服务器端排序控件 | ✅ | ❌（客户端列排序） | ❌ | P2 |
| 搜索范围 base/one/sub | ✅ | ✅ | ✅ | — |
| 结果批量操作（删除/移动） | ✅ Batch Operations Wizard | ❌（单条操作） | ❌ | P1：结果多选批量删除/移动 |

## 6. 目录浏览与条目编辑

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| DN 树浏览 + 懒加载 | ✅ | ✅ 虚拟列表 + 计数徽章 | ✅ | — |
| 树关键字过滤 | ✅ | ✅（远程子树过滤） | ✅ | — |
| 条目查看/编辑（表单） | ✅ | ✅ 属性增删改 + 多值行 | ✅ | — |
| LDIF 视图（直接编辑生效） | ✅ | 🔶 只读 LDIF tab | 🔶 | P1：LDIF 编辑 → apply |
| 新条目向导（objectClass 模板） | ✅ | ✅ 模板向导 + schema must 铺开（M6-N4） | ✅ | JSON 自定义模板引擎不做 |
| 二进制属性（图片/hex/base64 查看器） | ✅ | ✅ 预览/hex/PEM 三视图 + 上传（M6-N3） | ✅ | — |
| 密码修改扩展操作（passwd） | ✅ | ❌（走 modify userPassword，前端哈希辅助已落地 M6-N2） | 🔶 | P2：RFC 3062 extend |
| 条目复制/粘贴、书签 | ✅ | ❌ | ❌ | P2 |
| 子树删除（递归 + 子条目计数确认） | ✅ | ✅ 递归删除（Tree Delete 控件 + 回退）+ childrenCount 确认（M6-N1，收编自 PLA 对账表） | ✅ | — |
| DN 重命名/移动子树 | ✅ | ✅ modifyDn | ✅ | — |

## 7. 导入 / 导出

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| LDIF 导出（条目/子树/结果） | ✅ | ✅（结果 + 子树） | ✅ | — |
| CSV/JSON 导出 | ✅（CSV/XLSX） | ✅（CSV/JSON） | ✅ | — |
| LDIF 导入 | ✅ | ❌ | ❌ | P0/P1：前端 LDIF 解析 → 逐条 entry/add（含 changetype 拒绝策略） |
| DSML 导入导出 | ✅ | ❌ | ❌ | P2（不做，按需） |

## 8. Schema / RootDSE

| 能力 | ADS | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| Schema 浏览（属性/objectClass 列表） | ✅ | ✅ SchemaPanel 双列 | ✅ | — |
| objectClass must/may 明细 | ✅ | ✅（schemaCache 元数据） | ✅ | — |
| 属性语法/相等匹配规则明细 | ✅ | ❌（仅名称/描述） | 🔶 | P2：schema 原始定义透出 |
| RootDSE 查看 | ✅ | ✅ | ✅ | — |

## 9. 安全策略 / 审计（超越 ADS 的部分）

| 能力 | ADS | 本插件 | 状态 |
| --- | --- | --- | --- |
| DN 白名单（读/写分离） | ❌ | ✅ | ✅ 超越 |
| 屏蔽属性（读过滤 + 写拒绝） | ❌ | ✅ | ✅ 超越 |
| 只读门禁 | 🔶 | ✅（表单 ∥ 宿主双层） | ✅ |
| 写操作审计（事件 + jsonl 落盘） | 🔶（change logs） | ✅ audit feed 面板 | ✅ |

## 10. UI 体验

| 能力 | ADS（SWT 富客户端） | 本插件 | 状态 | 计划 |
| --- | --- | --- | --- | --- |
| 主题跟随宿主（亮/暗 + 令牌） | ❌（Eclipse 主题） | ✅ 宿主 1.1 theme 通道 | ✅ | — |
| 七语 i18n | ✅ 多语 | ✅ 七语（en/es/it/ja/pt-BR/zh-CN/zh-TW） | ✅ | — |
| 键盘操作（↑/↓/Enter 打开条目） | ✅ | ✅ | ✅ | — |
| 列宽持久化 | ✅ | ✅ | ✅ | — |
| UI 自动化测试 | ❌（手工/ SWTbots） | 🔶 vitest 单测 + mock 走查；本轮补组件测试 + 浏览器走查脚本（scripts/ui_test.mjs，入 test.sh） | ✅（本轮） | 持续：关键流 e2e（shared/host-e2e） |

## 11. 追赶路线（并入 IMPL_PLAN M5/M6/M7）

- **M5-a（本轮）**：TLS 字段联动显隐 + 连接字段排版重排；≠ 运算符；
  UI 测试双轨（组件测试 + ui_test.mjs 走查脚本）。
- **M5-b**：NOT 组 UI；连接检查 `ldap/check`（network/bind 分级）；
  搜索历史；LDIF 编辑生效。
- **M6（扩容后，与 PLA 对账表 N1–N4 并轨）**：LDIF 导入；结果批量操作；
  新条目 objectClass 模板（N4）；二进制属性查看器 + 上传（N3）；
  子树删除（N1）；密码哈希辅助（N2）。任务分解见 PLA 对账表 §7（L6-x）。
- **M7（按需）**：mTLS 客户端证书；CRAM-MD5；referral 策略；服务器端排序；
  digest realm；DSML。
