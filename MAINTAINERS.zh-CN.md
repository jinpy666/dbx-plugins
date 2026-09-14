# 插件维护规约

## 所有权模型

- 四个插件目录是四个独立发布单元。
- `shared/frontend/` 是公共宿主适配的唯一实现点。
- `host/` 是上游维护的子模块，默认只读。
- Marketplace 签名和 catalog 发布属于 DBX Store，不属于插件源码维护者。

## 必须执行的维护工作

### 每次代码变更

- 同步对应协议文档和实施文档。
- 用户可见文案保持七语完整。
- 运行相关单测、前端检查和 smoke。
- 检查 permissions、secret binding、审计内容和日志。
- 合并前运行仓库卫生扫描。

### 每次宿主更新

宿主子模块或 DBX.app 更新后运行 `scripts/host-sync.sh contract`，检查 binary
事件、filesystem、appearance/locale、workbench state、manifest lifecycle 和
mock。优先使用 optional 降级，不要硬依赖更新版本的宿主 API。

### 每次插件发布

- 使用新的不可变 SemVer，禁止原地重建已发布版本。
- 使用 `<plugin>-v<version>` tag。
- 为五个 native target 生成 unsigned 候选包。
- 校验候选 metadata、包 checksum、permissions 和 Manifest identity。
- 提交 DBX Store 审核并由其完成仓库签名。
- 记录 release notes、已知限制和回滚信息。

## 依赖与安全事件

Dependabot 更新由对应插件维护者审核。安全更新优先于功能工作，并应附带回归
测试，或说明为什么不适合自动化测试。

凭据暴露时先撤销或轮换，保留的证据必须脱敏，然后按照
[SECURITY.zh-CN.md](SECURITY.zh-CN.md) 处理。重写 Git 历史不能替代凭据轮换。
