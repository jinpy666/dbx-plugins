# DBX Plugins

[English](README.en.md) · [贡献指南](CONTRIBUTING.zh-CN.md) ·
[维护规约](MAINTAINERS.zh-CN.md) · [安全策略](SECURITY.zh-CN.md) ·
[MCP 使用指南](docs/MCP_USAGE.zh-CN.md)

DBX Plugins 为 DBX 提供面向基础设施、目录服务、文件存储和消息平台的专业工作台。
每个插件都可以独立安装、配置、升级和发布。

![DBX 插件中心](shared/host-e2e/screenshots/02-plugin-center.png)

## 插件一览

| 插件 | 适用场景 | 核心能力 |
|---|---|---|
| [SSH & SFTP](ssh/README.md) | 服务器运维 | 终端、远程命令、SFTP、ProxyJump、Known Hosts、Quick Sudo |
| [LDAP](ldap/README.md) | 目录管理 | DN 树、RFC 4515 搜索、条目编辑、Schema、LDIF/CSV 导出 |
| [Files](files/README.md) | 文件与对象存储 | 本地、S3、OSS、WebDAV、FTP、SFTP、SMB、同步与大文件传输 |
| [Kafka](kafka/README.md) | 消息平台运维 | topic、消息、消费组、lag、offset、ACL、Schema Registry |

所有插件均支持简体中文、繁体中文、英语、西班牙语、意大利语、日语和葡萄牙语，
并通过 DBX 宿主的 secret binding 管理连接凭据。

## 快速开始

安装 DBX 插件 CLI：

```bash
npm install --global @dbx-app/plugin-cli
```

运行目标插件验证：

```bash
ssh/scripts/test.sh --skip-host
ldap/scripts/test.sh
files/scripts/test.sh
kafka/scripts/test.sh
```

运行工作区级快速检查：

```bash
shared/test-all.sh --quick
python3 shared/release/check_repo_hygiene.py
```

多平台构建由 [Build multi-platform plugin candidates](.github/workflows/build-candidates.yml)
负责，覆盖四插件 × 五个 DBX 原生 target，并在 CI 中上传 `.dbxp`、`.artifact.json`
和合并候选包。发布流程与官方 DBX plugin release reusable workflow 对齐，详见
[多平台 CI 与发布产出](docs/CI_MULTI_PLATFORM.zh-CN.md)。

## 目录结构

```text
ssh/       SSH 与 SFTP 插件
ldap/      LDAP 插件
files/     多后端文件存储插件
kafka/     Kafka 管理插件
shared/    公共宿主适配、测试和发布检查
scripts/   工作区维护与发布脚本
host/      DBX 宿主子模块
```

## 发布与安全

四个插件是独立发布单元，使用 `<plugin>-v<version>` 形式的 Git tag。作者 CI
生成候选包，DBX Store 负责审核、签名和 Marketplace catalog 发布。

凭据、私钥、连接导出、本地 app-data、构建产物和运行时状态不得提交到仓库。
疑似漏洞请按照 [SECURITY.zh-CN.md](SECURITY.zh-CN.md) 私下报告。

公开发布前的许可证、版权主体和 publisher 信息必须由维护者确认，详情见
[公开发布检查清单](docs/REPO_PUBLISH.zh-CN.md)。
