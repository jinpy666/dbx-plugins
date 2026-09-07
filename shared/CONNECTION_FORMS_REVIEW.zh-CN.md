# 四插件连接表单组合审查

任务：整理 SSH、LDAP、Files、Kafka 的连接方式、认证与加密组合，减少无关输入，
使可见字段、必填校验和实际连接参数一致。以各插件 manifest 与 sidecar 为准；
沿用宿主 `visible_when` / `required_when`，不新增表单协议。

| 插件 | 审查发现 | 本轮处理与验证 |
| --- | --- | --- |
| SSH | 2FA 的 OTP 输入在仅密码模式仍出现；sudo 与认证字段分散；多行白名单使用单行输入 | 按依赖排序；OTP 按流程显隐；保留全局 sudo 凭据和只读连接登录 2FA 语义 |
| LDAP | Kerberos 密码在 keytab/ccache 模式仍显示；路径不必填；关闭 TLS 后残留 CA 仍可能触发文件读取；端口说明与必填规则矛盾 | 凭据类型级联；TLS 关闭不消费证书；默认端口按协议推导，显式端口保留 |
| Files | 原生 SFTP 无独立用户输入；私钥内容可落 config；FTP 的 FTPS 端口提示错误；SSH 连接 id 字段无实现消费方 | 补用户输入；私钥迁至 secret 并兼容旧数据；纠正显式 FTPS 21 端口；移除无效的重复隧道配置 |
| Kafka | ZooKeeper 仍要求 bootstrap；Kerberos/OAuth 显示 SASL 账密；OAuth 来源未选可提交；退出 SASL 后旧 OAuth 值仍阻止连接；私钥单行输入破坏 PEM | 按连接来源/机制级联并校验；隔离非激活分支；多行私钥；核对 TLS/SASL/Registry 交叉组合 |

验收：四插件真实 manifest 组合矩阵、宿主保存/重新载入及多行 secret 测试、
对应 sidecar 参数回归、七语字段/选项覆盖、各插件 `scripts/test.sh` 与 smoke。
运行结果随收尾补充；各插件实施文档和 PROGRESS 链接到本记录。
