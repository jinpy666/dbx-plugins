# 三插件 vs tiny-rdm 功能拉平总对标（2026-08-29）

基线：`/Users/Jinpy/GolandProjects/tiny-rdm`（本地源码，ssh/sftp 服务最后改于
2026-07-15，早于本对标收口，无方法漂移）。
对象：`ssh/`、`files/`、`ldap/` 三插件。
方法：三路并行盘点（tiny-rdm 服务源码 ↔ 插件协议面/前端），关键结论已抽点复核。
原则沿用 `ssh/docs/FEATURE_PARITY.zh-CN.md`：宿主已承担的能力（连接管理、
profile 持久化、审批流、隧道/代理传输层、secret 存储）不重复实现，不计缺口。

## 总览

| 插件 | 对标源 | 结论 | 详见 |
| --- | --- | --- | --- |
| ssh-sftp | ssh_service.go / sftp_service.go / sudo_exec_service.go / ssh_metrics_service.go | **基本拉平**；唯一实质出入 DownloadSudo 口径已修正（见 §1） | `ssh/docs/FEATURE_PARITY.zh-CN.md`（本对标不重复其表） |
| files | fs_service.go / sftp_service.go / 前端 ToolsFile 族 | **核心 CRUD/传输/安全门禁拉平或更强**；差距集中在 chmod/打包/zip/搜索/About/分页接入，另有 1 个内部自洽缺口（`files/audit/list` 未实现） | §2 |
| ldap | ldap_service.go（2094 行单文件 → 插件分层移植） | **认证矩阵/浏览/搜索/条目编辑/导出拉平**；缺口为 MCP 工具面、mTLS、Kerberos SPN 定制、真实 KDC 集成验证 | §3 |

---

## 1. ssh-sftp：复核结论

既有对标文档（2026-08-29 收口口径）与两侧代码一致，tiny-rdm 无文档遗漏的新能力。
本轮已修正 `FEATURE_PARITY.zh-CN.md` 两处口径：

1. **DownloadSudo**：原文将 DownloadSudo 列入 sudo 族「✅ 已有」，实际插件无
   `sudo/download`（main.rs sudo 族 11 方法；传输槽无 sudo 路径）。已改为如实标注：
   大体积 root 文件二进制下载未做，现只能退化为 `sudo/readFile`（exec+base64，受包尺寸限制）。
2. **UpdateProfileQuickSudo / UpdateProfileSSHExecution**：补一行「不做」——功能被
   `ssh/settings`（quickSudo/sudoUsePty 等，ssh.rs 会话级）覆盖，profile 持久化归宿主。

维持 deferred 的项（非缺口，决策登记在案）：端口转发 -L/-R（归属决策未定）、多会话
分屏、最近关闭恢复、远程 SQL 会话（宿主承担）、隧道/跳板（整合 DBX 传输层）。

## 2. files：拉平对照与差距

### 2.1 已拉平（或更强）的能力

- **核心 CRUD**：list/listPaged/stat/read（带 truncated）/write/mkdir/rmdir/delete/purge/
  rename/copy/move/syncDir/copyDir 全对齐 `fs_service.go`；copy/move/rename 具
  native↔异步 job 双路降级（tiny-rdm 为同步单文件）。
- **上传下载**：二进制通道 start/finish + `files/transfer/progress` 事件（200ms/1% 节流）、
  取消、统一 transfers/list（单文件 job + 持久化历史 + dir job）。
- **预览**：text/image/hex/binary/archive 五模式，强于 tiny-rdm 的文本+二进制判断。
- **安全门禁**：read_only × allow_delete × root 白名单（policy.rs）全矩阵，
  dangerousPaths 分级确认 + root-purge 拒绝，严于 tiny-rdm 的 LockToRoot。
- **归档**：archiveList（分页 + tar-bomb 防护）/extract（zip-slip 防护，小包同步、超限降级 job）。

### 2.2 差距（gap）

高：
1. **`files/audit/list` 未实现（内部自洽缺口）**：`frontend/src/components/AuditPanel.vue:27`
   调用 `files/audit/list`，`backend/src/main.rs` 无该方法臂（已 grep 全库确认），
   审计面板恒显「不可用」。audit.jsonl 落盘数据齐全，补一个只读分发臂即可闭环。
2. **Chmod/权限修改**：tiny-rdm 三处实现（fs/sftp/sudo Chmod + attributes UI）；
   插件协议与 FileEntry 数据模型均无 mode 字段。
3. **zip 归档**：archiveList/extract 对 zip 均报 Phase 2（PROGRESS-B-ARCHIVE 登记）；
   tiny-rdm 侧仅支持 tar，但 zip 是日常最常见格式。

中：
4. 磁盘空间/配额 About（tiny-rdm `About`:1071；插件 capabilities 只透出 OpenDAL 能力位）。
5. 前端接 `files/listPaged`（后端已备，App.vue 仍全量 `files/list`，大目录有性能风险）。
6. 归档创建/打包（tiny-rdm `Archive`:850 远端 tar -czf）。
7. ~~文件名搜索/过滤~~ ✅ 2026-08-29 补齐：工具栏过滤框（`lib/searchFilter.ts` +
   FileToolbar 搜索框，大小写不敏感子串，七语）。
8. 新建空文件入口（后端 `files/write` 空内容可覆盖，缺 UI 动作）。
9. PublicLink 前端入口（后端 `files/publicLink` 已实现，前端无调用）。
10. `files/read` 无 offset 分段读（tiny-rdm ReadFile 支持 offset+length 续读）。

低：
11. 视图模式（网格/列表）；12. 后退/前进路径历史；13. 书签/快速路径；
14. 剪贴板式 copy/cut/paste 交互模型（现有对话框+拖拽功能等价）；
15. 传输速度/时长显示（TransferRecord 带 Speed/Duration）；16. 属主/权限列展示
（连带 chmod 数据模型）；17. exists/pwd/touch 独立方法（语义可由 stat/write 覆盖）。

## 3. ldap：拉平对照与差距

### 3.1 已拉平（或更强）的能力

- **认证矩阵**：anonymous/unauthenticated/simple/NTLM/NTLM-hash/DIGEST-MD5/Kerberos
  GSSAPI/SASL EXTERNAL 八种全移植（dial.go bindLDAPConnection）；GSSAPI 客户端
  `internal/ldapgssapi` 与 tiny-rdm 源 diff 零差异；smoke_auth 9 PASS + 1 SKIP（A2
  DIGEST-MD5 受容器服务器限制，tiny-rdm 同样受限）。
- **浏览/搜索/编辑**：search（filter 校验/scope/deref/sizeLimit/typesOnly/分页）、
  entry/get、rootDse（白名单禁用语义沿袭）、schema（另加服务端 10min 缓存）、
  entry/add|modify|delete|modifyDn，写白名单 × 11 项默认屏蔽属性原样移植。
- **导出/编辑器**：LDIF（RFC 2849，两侧均不支持 changetype）/CSV/JSON 导出、
  EntryEditor LDIF⇄表双向、可视化过滤器构建器（63 单测）。
- **插件独有增强**：`ldap/count` 树徽章精确计数（tiny-rdm 用 sizeLimit 500 截断值）、
  search truncated 上限标记、denied/error 审计粒度、tls-insecure 审计、预设跨端持久化
  （presets.json vs localStorage）。

### 3.2 差距（gap）

| # | Gap | 重要度 |
| --- | --- | --- |
| 1 | MCP 工具面缺失：tiny-rdm 有 10 个 `ldap_*` MCP 工具 + 输出脱敏 + 白名单策略 + e2e 测试；插件 main.go:11 明确 mcp/* 本期不做（M4 未启动）。**前置条件（2026-08-29 复核）**：宿主 worktree 的 dbx-mcp 尚无通用插件工具桥（`dbx_list_plugin_tools`/`dbx_call_plugin_tool` 未落地，AppState.plugins 仅指 JDBC driver 插件）——ssh-sftp 已按该桥契约实现 mcp/tools+mcp/call；ldap/files 待宿主桥可用后按同契约补齐（工具清单见 ldap IMPL_PLAN §9 M4 / tiny-rdm tools_ldap.go），否则为死代码 | 高（自动化/AI 工具面需要时；纯工作台可延后） |
| 2 | Kerberos 真实 KDC 集成验证缺失：tiny-rdm 有 FreeIPA compose + 412 行 GSSAPI 集成测试；插件仅死 KDC 网络错误面 smoke（A5） | 中（企业 AD/FreeIPA 上线前风险点） |
| 3 | TLS 客户端证书（mTLS）不支持：tiny-rdm buildTLSConfigFromCommon 加载 X509KeyPair；插件 TLS 仅 CA/SNI/verify，影响 external-over-TLS 组合 | 中 |
| 4 | Kerberos SPN 定制字段缺失（ExplicitSPN/ServiceName/ServiceHost/BypassRewrite 随 D5 删除，SPN 固定 `ldap/<host>`） | 中（可借宿主 DNS/别名缓解） |
| 5 | ldapi（SASL EXTERNAL）成功路径无容器级验证（A2 SKIP，仅单测覆盖调用面） | 低 |
| 6 | 搜索预设文本导入/合并未移植（searchPresets.js parseSearchPresetImportText/merge） | 低 |
| 7 | DIGEST-MD5 无真实服务器验证（容器无该机制，与 tiny-rdm 同限） | 低 |

宿主承担不计缺口：profile CRUD/加密凭据（secret binding）、写审批流（插件以审计
+ 事件替代）、SSH/代理隧道与 endpoint rewrite、连接池多窗口注册。

---

## 4. 建议收口顺序（如做实现轮）

1. files：补 `files/audit/list` 只读臂（自洽缺口，最小改动、立即恢复审计面板）。
2. ssh-sftp：补 `sudo/download`（临时 sudo cp → SFTP 下载，对齐 PrepareSudoDownloadTemp）
   或维持现状（已如实标注）。
3. files：chmod 数据模型 + 属性编辑；zip 归档支持（Phase 2 既定方向）。
4. files：前端接 listPaged、搜索过滤、About 空间信息。
5. ldap：mTLS（tls_cert_path/tls_key_path 两 manifest 字段 + dial.go 接线）。
6. ldap：MCP 工具面（M4）、FreeIPA KDC 集成测试。

> 本对标为盘点文档，不代表以上项已实施；实施状态以各插件 PROGRESS 文档为准。
