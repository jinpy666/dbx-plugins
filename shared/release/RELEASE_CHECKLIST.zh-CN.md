# DBX 插件发布清单

## 发布前

- [ ] 只修改目标插件；确认没有把另外三个插件的变更带入 Release。
- [ ] `manifest.json`、sidecar 版本和 tag 一致。
- [ ] `source`、`homepage`、publisher、permissions 已确认。
- [ ] 协议文档与实施/进度文档已同步。
- [ ] 七语文案完整。
- [ ] 单元测试通过。
- [ ] 前端 typecheck、test、build 通过。
- [ ] smoke 通过；环境不可用的场景明确记录为 SKIP。
- [ ] 凭据不进配置、日志、审计和 artifact。
- [ ] 运行 `shared/release/validate_manifests.py <plugin> --tag <tag>`。

## 候选包

- [ ] GitHub Release 使用 `<plugin>-v<semver>` tag。
- [ ] 生成五个 native target 的 unsigned `.dbxp`。
- [ ] 每个 `.artifact.json` 的 SHA-256 和 size 正确。
- [ ] candidate 不包含 `signature.json`。
- [ ] `release-candidates.json` 只包含当前插件。

## 发布后自动同步（DBX Store）

- [ ] GitHub Release 已发布（含候选包清单和 release notes），等待自动同步。
- [ ] 核对 catalog 中的 plugin ID、version、publisher、permissions。
- [ ] 核对最终 signed artifact 的 `signingKeyId`、SHA-256、size。
- [ ] 官方 catalog smoke 通过。
- [ ] 用干净 app-data 安装并完成一次连接/工作台冒烟。
- [ ] 记录回滚版本和已知限制。
