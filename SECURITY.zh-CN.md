# 安全策略

## 报告漏洞

不要在公开 Issue 中报告疑似漏洞。请使用本仓库的私有 GitHub Security Advisory：

<https://github.com/jinpy666/dbx-plugins/security/advisories/new>

报告应包含受影响插件和版本、影响、复现步骤、已移除凭据的日志以及建议严重
等级。不要上传私钥、密码、生产数据或完整连接导出文件。

如果私有 Advisory 不可用，请通过 GitHub 仓库主页联系维护者并请求私下沟通渠道。

## 范围

可以报告插件代码、sidecar、构建/打包 workflow、secret 处理、宿主桥调用和
发布完整性问题。如果根因属于 DBX 宿主，应同时向上游 DBX 项目报告。

## 凭据与测试数据

只使用一次性本地或容器凭据。禁止提交真实凭据、私钥、token、客户数据或发布
签名密钥。任何秘密一旦意外暴露，即使后来删除提交，也必须立即轮换。
