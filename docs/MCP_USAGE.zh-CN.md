# DBX 插件 MCP 使用指南

四个插件都提供 MCP 能力。推荐优先使用 DBX MCP 桥：连接、凭据、只读策略和审计
由 DBX 宿主管理；需要脱离 DBX 工作台运行时，再使用 sidecar 的独立 stdio 模式。

## 选择连接方式

| 模式 | 适合场景 | 凭据来源 | 推荐度 |
|---|---|---|---|
| DBX MCP 桥 | 生产环境、保存连接、需要宿主策略 | DBX 已保存连接 | 推荐 |
| 独立 stdio | 本地脚本、CI、临时诊断、开发调试 | 每次调用内联或环境变量 | 适合开发 |

### DBX MCP 桥

1. 在 DBX 中安装并启用目标插件。
2. 启动 DBX MCP 服务（桌面内置 MCP 或 `dbx mcp`）。
3. 通过 `dbx_list_plugin_tools` 查看已安装插件的工具和 JSON Schema。
4. 使用 `dbx_call_plugin_tool` 调用工具，连接引用使用 DBX 中保存的
   `connectionId`。

这种模式下，MCP 客户端不需要接触密码、私钥或 Kerberos 凭据。

## 独立 stdio 配置

先构建目标插件，sidecar 路径如下：

```text
ssh/backend/target/release/dbx-plugin-ssh
files/backend/target/release/dbx-plugin-files
ldap/backend/bin/dbx-plugin-ldap
kafka/backend/bin/dbx-plugin-kafka
```

在支持 `mcp.servers` 的客户端配置中加入以下示例。将
`/absolute/path/to/dbx-plugins` 替换成仓库绝对路径；不要把密码直接写进配置文件。

```json
{
  "mcp.servers": {
    "dbx-ssh": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/ssh/backend/target/release/dbx-plugin-ssh",
      "args": ["--mcp"],
      "env": {
        "DBX_SSH_MCP_READ_ONLY": "1"
      }
    },
    "dbx-files": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/files/backend/target/release/dbx-plugin-files",
      "args": ["--mcp"],
      "env": {
        "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.files"
      }
    },
    "dbx-ldap": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/ldap/backend/bin/dbx-plugin-ldap",
      "args": ["--mcp"],
      "env": {
        "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.ldap"
      }
    },
    "dbx-kafka": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/kafka/backend/bin/dbx-plugin-kafka",
      "args": ["--mcp"],
      "env": {
        "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.kafka"
      }
    }
  }
}
```

不同客户端的外层键名可能是 `mcp.servers`、`mcpServers` 或配置界面字段，
但每个服务器的 `command`、`args` 和 `env` 含义相同。命令必须使用绝对路径；
Windows 请填写 `.exe` 路径。

配置或重新构建 sidecar 后，必须重启 MCP 会话。stdio 进程不会热加载新的二进制。

## 第一次调用

MCP 客户端连接后，按以下顺序工作：

1. `initialize`
2. `notifications/initialized`
3. `tools/list`
4. `tools/call`

可以先用自然语言验证工具是否可见：

```text
列出当前 MCP 服务器提供的工具，并告诉我哪些工具是只读的。
```

## 四个插件怎么用

### SSH & SFTP

常用工具：`ssh_exec`、`ssh_metrics`、`sftp_list`、`sftp_upload`、
`sftp_download`。生产环境建议让 MCP 服务器以
`DBX_SSH_MCP_READ_ONLY=1` 启动，或者使用 DBX MCP 桥继承连接级审批和 sudo 白名单。

示例请求意图：

```text
使用 SSH 连接检查 /var/log/app 下最近修改的 20 个文件，只读，不执行写操作。
```

### Files

常用工具：`files_scan_digest`、`files_cursor_next`、`files_write`、
`files_mkdir`、`files_rename`、`files_delete`。

本机测试可以使用无凭据连接：

```json
{
  "connection": { "protocol": "local", "root": "/tmp/dbx-files" },
  "path": "/",
  "format": "rows"
}
```

大目录先使用 `files_scan_digest`，收到 `cursorId` 后再使用
`files_cursor_next`，不要一次性拉取整个目录树。删除和 purge 需要两阶段确认。

### LDAP

常用工具：`ldap_search_digest`、`ldap_cursor_next`、`ldap_ui_schema`、
`ldap_entry_write`。

搜索时先限定 `baseDn`、`scope` 和属性投影；大结果使用 `format: "rows"` 加
cursor 翻页。删除条目或子树需要先取得 `confirmToken`，再用完全相同的参数确认执行。

独立 stdio 模式适合 simple/anonymous 等内联认证；Kerberos 等复杂认证优先使用
DBX 中保存的连接并通过 MCP 桥调用。

### Kafka

常用工具：`kafka_messages_digest`、`kafka_cursor_next`、
`kafka_messages_produce`、`kafka_groups_offsets_reset`、`kafka_topics_delete`。

Kafka 独立 stdio 默认只读。需要写入时显式传入：

```json
{
  "brokers": ["127.0.0.1:9092"],
  "readOnly": false,
  "topic": "dbx-events",
  "value": "{\"event\":\"test\"}"
}
```

删除 topic、清理消息和重置消费组 offset 还需要 `allowDelete: true`，并遵循两阶段
确认。stdio 工具不会替你猜 topic 名；先提供准确 topic，或在 DBX 工作台中查看 topic。

## 安全边界

- 独立 stdio 的内联凭据会进入 MCP 工具参数和客户端上下文，只适合本机可信会话。
- 生产环境优先使用 DBX MCP 桥，让宿主解析连接和执行权限策略。
- 删除、清理、offset 重置和条目删除都需要显式确认。
- MCP 响应有大小上限；出现 `truncated: true` 时收窄过滤条件或使用 cursor。
- `*_ui_*` 工具依赖 DBX 工作台，独立 stdio 模式下可能返回 `UNAVAILABLE`。

## 验证 MCP 配置

```bash
cd ssh && python3 scripts/smoke_mcp.py
cd ../files && python3 scripts/smoke_mcp.py
cd ../ldap && python3 scripts/smoke_mcp.py
cd ../kafka && python3 scripts/smoke_mcp.py
```

完整工具表和协议细节见各插件的 `docs/MCP.zh-CN.md`。
