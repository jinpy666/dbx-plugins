# DBX 插件贡献指南

## 修改前

1. 阅读目标插件 README 和相关公开维护规约。
2. 确认改动属于现有四个插件之一；未经明确决策不要新增插件族。
3. 先执行 `git status`，保留与本任务无关的本地修改。

## 开发规约

- sidecar 协议使用 `<域>/<动作>`，字段使用 camelCase。
- 宿主兼容代码统一放在 `shared/frontend/`，禁止四个插件各复制一份。
- 凭据只通过宿主 secret binding 管理，不写日志、不落盘、不进入审计内容。
- 代码变更必须同步协议文档、PROGRESS/任务清单和七语文案。
- 单测保持确定性和离线；容器或真实服务测试必须明确标注。
- 外部环境不可用时使用并记录 `SKIP`，不能静默吞掉失败。

## 验证

运行目标插件的 `scripts/test.sh`。提交 PR 前还应运行：

```bash
shared/test-all.sh --quick
python3 shared/release/check_repo_hygiene.py
```

## Pull Request

说明受影响插件、协议变化、安全影响和验证结果。不要提交 `.dbxp`、构建目录、
截图、本地 app-data、凭据或私钥。发布相关变更必须遵循
[发布清单](shared/release/RELEASE_CHECKLIST.zh-CN.md)。
