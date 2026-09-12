# 仓库发布记录（GitHub 私有仓库）

**发布日期**：2026-09-12　**仓库**：`jinpy666/dbx-plugins`（private）

本文档记录发布 GitHub 私有仓库时的敏感信息清理决策与规则，后续向该仓库
推送或转公开前，先对照本清单复查。

## 发布前敏感信息排查结论

| 检查项 | 结论 |
|---|---|
| API token / 私钥 / 云凭证 | 全仓库与全部 git 历史无命中（仅 AWS 官方文档示例 key，无害） |
| 硬编码密码 | 无。所有凭证经环境变量传入；`adminpassword` 等仅为本地测试容器默认值 |
| git 提交作者邮箱 | 原为 `jinpy.he@kuehne-nagel.com`（全部 84 提交），已重写 |
| 内网 IP / 部署细节 | copilot-pool 内含，已整体移出 |
| 个人本机用户名（`/Users/jinpy`） | 仅测试 fixture 中的本机路径，保留 |

## 处置记录

1. **git 历史重写**（`git filter-repo`，发布时尚无远程，历史重写零成本）：
   - 作者/提交者邮箱全部替换为 `jinpy666@users.noreply.github.com`；
   - 从全部历史移除 `copilot-pool/` 与 `docs/PRINTER_RESET_GITHUB_KB.zh-CN.md`。
2. **copilot-pool/**（Copilot 账号池桥接工具）：整体排除出发布仓库，
   本地与服务器留存；已加入 `.gitignore`。后续不在该仓库恢复跟踪
   （含内网服务器 IP 与部署细节，且账号池工具不适合托管在 GitHub）。
3. **docs/PRINTER_RESET_GITHUB_KB.zh-CN.md**（打印机重置第三方调研笔记）：
   与 DBX 插件项目无关，移出仓库；已加入 `.gitignore`。
4. **仓库级 git 身份**已固定为 `jinpy666 <jinpy666@users.noreply.github.com>`，
   后续在本仓库的提交不会带入公司邮箱。
5. 以上移出内容的本地备份：`~/btroot/dbx-plugins-local/`（仓库外）。

## 转公开前复查清单

- [ ] `git log --all --format='%ae %ce' | sort -u` 只含 noreply 邮箱；
- [ ] 重新全仓 grep token / 私钥 / 内网 IP 模式；
- [ ] 确认 `host/` 子模块（t8y2/dbx）公开可见性可满足克隆需要；
- [ ] 复查各插件 `docs/PROGRESS-*.md` 中的真实环境信息（IP、主机名、账号）。
