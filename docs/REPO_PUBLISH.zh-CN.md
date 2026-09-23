# 仓库公开发布检查清单

本清单用于将 DBX 插件仓库从私有开发状态转换为公开开源项目。不要在本文档
记录真实 token、私钥、客户数据、内网地址、个人本机路径或内部部署信息。

## 代码与历史

- [ ] `python3 shared/release/check_repo_hygiene.py` 通过。
- [ ] `git ls-files` 不包含 `.env`、密钥、证书、数据库、连接导出、运行时状态
      或构建产物。
- [ ] `git log --all --format='%ae %ce'` 不包含不应公开的工作邮箱。
- [ ] 所有与项目无关的调研、账号管理、部署和内部工具均已移出仓库。
- [ ] `host/` 子模块指向公开可访问、与当前插件 SDK 兼容的上游版本。

## 许可证与身份

- [ ] 维护者确认版权主体。
- [ ] 维护者确认软件许可证，并添加根目录 `LICENSE`。
- [ ] 四个 Manifest 的 publisher、source、homepage 和 license 一致且准确。
- [ ] 第三方依赖许可证和 NOTICE 要求已审核。

## 开源治理

- [ ] 双语 README、贡献指南、行为准则、安全策略和支持说明已发布。
- [ ] CODEOWNERS、Issue 模板、PR 模板和依赖更新已启用。
- [ ] 仓库卫生 CI 与 Manifest CI 通过。
- [ ] 安全报告入口已验证，不要求公开披露漏洞细节。

## 发布与供应链

多平台候选包由 [多平台 CI 与发布产出](CI_MULTI_PLATFORM.zh-CN.md) 自动构建和校验；
本节清单用于人工发布核对与 DBX Store 自动同步前的确认。

- [ ] 每个插件使用不可变的 `<plugin>-v<version>` tag。
- [ ] 五个平台的 unsigned candidate 均可重现构建。
- [ ] 候选包 metadata 的 hash、size、plugin ID、version 和 target 已校验。
- [ ] 候选包不包含 `signature.json` 或任何官方签名密钥。
- [ ] GitHub Release 已发布，DBX Store 自动同步（签名与 catalog）完成。

## 公开前最终动作

- [ ] 在干净 clone 中完成 README 快速开始。
- [ ] 在干净 app-data 中完成至少一个插件安装冒烟。
- [ ] 检查所有公开 URL、仓库权限、分支保护和 Actions 权限。
- [ ] 记录当前版本、已知限制和回滚版本。
