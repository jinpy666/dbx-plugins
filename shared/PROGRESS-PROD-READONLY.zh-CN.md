# 生产/只读连接适配收口（2026-08-29）

## 背景与决策

需求：连接设置中的「生产模块（禁止写，只读）」要与各插件类型适配，触发写动作时禁用相关写操作。

排查结论（宿主 worktree 实读）：

- 宿主连接配置有 `is_production` / `production_databases`（生产环境保护，面向数据库语义：
  库级作用域 + 写解锁只读豁免），且对插件类连接同样渲染该开关。
- `plugin_connection_params` 把整个 `ConnectionConfig` 下发给 sidecar（含 `is_production`）；
  但工作台 context 给插件前端的 `connection.readOnly` 为**硬编码 false**
  （`queryStore.ts` openPluginConnection），插件前端禁用链路从不生效。

**决策（按用户指示）**：宿主生产保护与插件语义存在定义冲突（库级作用域/写解锁豁免
对文件与目录服务不可映射），不改造宿主、不消费 `is_production`；改走**插件特定配置项**：

1. 各插件连接表单自带 `read_only` 开关（binding: config）：
   files / ldap 既有；ssh-sftp 本轮补齐（manifest 字段 + 6 语 label + en 默认 = 七语）。
2. 后端解析收敛：`表单 read_only(external_config) ∥ 宿主标准 read_only(ConnectionConfig)`
   —— 宿主通用只读勾选（非生产保护）向后兼容保留，ssh-sftp 原有行为、files/ldap 本轮对齐。
3. 后端为权威门禁，前端只读状态改为**后端透出**，不再依赖宿主 context：

## 插件改动清单

### ssh-sftp（Rust）
- `manifest.json`：connection-provider 新增 `read_only` boolean 字段 + 七语 label。
- `backend/src/model.rs`：from_lifecycle_params 只读收敛（external_config ∥ connection）；
  新增单测 `read_only_flags_from_host_and_form_force_read_only`。
- 写面门禁原已齐备（`ensure_writable` 覆盖 sftp createDirectory/write/rename/delete/chmod/
  touch/write_file/archive/extract/copy/move/upload；exec 写检测 :1386；sudo :1454）。
- `frontend/src/App.vue`：`connectionReadOnly` ref 取自 `ssh/sessions/list` 行 `readOnly`；
  `canWrite` 与只读徽章合并两路信号。

### files（Rust）
- `backend/src/model.rs`：只读收敛（external_config ∥ connection）；新增单测
  `read_only_flags_from_host_and_form_force_read_only_gate`。
- `backend/src/main.rs`：`files/capabilities` 响应新增策略层 `readOnly` 字段。
- `frontend/src/App.vue`：`canWrite` / 只读徽章并入 `capabilities.readOnly`。

### ldap（Go）
- `backend/internal/lifecycle/lifecycle.go`：Connection 结构体补宿主标准 `read_only` 字段。
- `backend/internal/ldapconn/service.go`：`profile.ReadOnly = 表单 ∥ 宿主 read_only`；
  `types.go` LDAPConnectionStatus 增加 `readOnly` 透出（SnapshotStatuses 填充）。
- `frontend`：`api.ts` status 行类型补 `readOnly`；App.vue `refreshBackendReadOnly()`
  从 `ldap/connections/statuses` 读取并并入 `canWrite`；mock（?ro=1）同步。
- 新增单测 `TestNewProfileFromLifecycleHostAndFormReadOnly`。

## 验证（2026-08-29 实测）

| 套件 | 结果 |
| --- | --- |
| ssh-sftp cargo | 110 passed（+1） |
| ssh-sftp 前端 typecheck/vitest | ✅ / 51 passed |
| files cargo | 94 passed（+1） |
| files 前端 typecheck/vitest | ✅ / 47 passed |
| ldap go vet + test | 四包全绿（+1 用例） |
| ldap 前端 typecheck/vitest | ✅ / 108 passed |

## 遗留

- 宿主若后续把工作台 context 的 `connection.readOnly` 修正为真实值
  （`read_only ∥ is_production`），插件端两路信号已兼容，无需再改。
- `is_production` → 只读映射按决策**未实施**（避免定义冲突）；如宿主端到端联调
  要求生产保护联动插件，再按 shared/FEATURE_PARITY_TINYRDM.zh-CN.md §4 排期。
