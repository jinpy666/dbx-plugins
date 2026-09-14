---
name: dbx-files-mcp
description: 通过 MCP 快速调用 DBX Files 工具（mcp__dbx-files__*）完成任务：浏览/扫描/查找文件、读写小文件、整理与删除目录。当用户要求对本地或远端文件系统做查找、统计、写入、改名、清理，或提到 files_scan_digest/files_write 等工具时使用。
---

# Files MCP 快速调用（mcp__dbx-files__*）

工具定位口诀：**读靠 digest，写靠 write/mkdir/rename，删必两阶段，UI 类不可用**。

## 工具地图（12 个）

| 意图 | 工具 | 要点 |
| --- | --- | --- |
| 看目录有什么/统计 | `files_scan_digest`（默认 digest 格式） | 聚合 + 样本 + stats（byExtension/largest/newest），一次拿全貌 |
| 精确列清单/翻页 | `files_scan_digest` `format:"rows"` → `files_cursor_next` | 每页 n≤20，翻到 `done:true` |
| 按名字/时间筛 | `files_scan_digest` `glob` / `modifiedSince`/`modifiedUntil` | `*` 不跨目录段，`**` 跨段；时间用 Unix 毫秒 |
| 常用位置 | `files_ui_quick_paths` | root/home/Desktop 等 stat 校验过的快捷路径，stdio 可用 |
| 写/覆盖小文件 | `files_write` | `dataBase64` 标准 RFC4648；空串=建空文件；建议 ≤1 MiB（硬上限 4 MiB） |
| 建目录 | `files_mkdir` | mkdir -p 语义 |
| 改名/移动单文件 | `files_rename` | **目录 rename stdio 不可用**（异步 job 依赖工作台） |
| 删文件 | `files_delete` | 两阶段：先拿 confirmToken（60s、绑参数 hash、一次性） |
| 清空整棵子树 | `files_purge` | 两阶段；**拒绝连接根与 `/`** |
| 驱动工作台面板 | `files_ui_focus/search/select/state` | stdio 恒 UNAVAILABLE——改用 scan_digest 或 DBX MCP 桥 |

## 连接寻址（标准顺序）

1. **本机路径** → `connectionId: "__local__"`（零依赖首选），或内联
   `connection: {"protocol": "local", "root": "/some/dir"}`（root 即扫描根，路径以 `/` 起算）。
2. **远端** → 内联 `connection` 对象：`s3`/`oss` 需 `bucket`+`endpoint`+`region`+`accessKeyId`+`secretAccessKey`；
   `sftp`/`sftp-native` 用 `user`+`password`/`key`；`webdav`/`ftp`/`smb` 用 `username`+`password`（smb 加 `share`）。
   凭据会进工具参数与上下文——仅限本机可信会话。
3. **DBX 保存连接** → 传保存的 `connectionId`（DBX 应用运行中自动桥转发，凭据不出参数）。

相同内联参数自动池化为 `mcp-inline-<hash>`，后续调用只传回该 id 即可。

## 硬限制

| 限制 | 值 | 超限动作 |
| --- | --- | --- |
| 单响应 | 16 KiB（`truncated:true`） | 改 rows+cursor、加 glob/时间窗、看 stats 而非全量 |
| files_write | 4 MiB 硬上限 | 大文件走 DBX 工作台传输通道 |
| cursor | n≤20，TTL 10 min，LRU 8 会话 | 过期重新 scan_digest |
| confirmToken | 60 s，参数一变即作废 | 重新 preview 拿新 token |
| 删除根 | purge 拒连接根与 `/` | 换子目录 |

## 高频坑

- 内联连接参数必须整包一致才会命中同一池化 id——时带时不带会建两个连接。
- `scanTruncated:true` 说明扫描条数超预算（默认 10 万条封顶）：结果只是子集，收窄范围再扫。
- 写远端前想清楚只读门：连接带 `readOnly:true` 时写工具直接被拒。
- 找「最近改过」用 `modifiedSince`（毫秒时间戳），不要全扫后自己排序。

更多背景（配置、桥接兜底、契约细节）见 skill `dbx-mcp-usage` 与
`shared/TEST_PLAN_MCP_ZCODE.zh-CN.md`。
