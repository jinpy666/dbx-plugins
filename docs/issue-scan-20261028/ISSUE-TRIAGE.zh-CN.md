# 四插件 Issue 扫描与跟进（2026-10-28）

> **终态（同日收尾）**：ssh 0.7.11-beta.1 与 files 0.1.92-beta.6 预发布已发
> （tag + GitHub Release，五平台构建成功/进行中）；ldap/kafka Windows 测试
> 基建修复已推送；四插件 `scripts/test.sh` Windows 本机全绿。回复草稿未经
> 用户确认，尚未发布到 GitHub issue。

扫描范围：jinpy666/dbx-plugin-{ssh,ldap,files,kafka} 全部开放 issue（共 16 个：
ssh 11 / files 5 / ldap 0 / kafka 0）。原始导出见本目录 `ssh-*.json`、`files-*.json`。

版本基线：ssh 0.7.10（正式）、ldap 0.1.100-beta.2、files 0.1.92-beta.5、
kafka 0.1.55-beta.1。

## 状态总览

| Issue | 标题 | 状态 | 本轮动作 |
| --- | --- | --- | --- |
| ssh#246 | MCP 控权无效（只读仍可写） | **未处理（安全）** | P0：定位 sidecar 写门禁，随 0.7.11-beta.1 预发布 |
| ssh#243 | 文件预览无滚动条 | 未处理（复现已齐） | P1：前端速修，随 0.7.11-beta.1 |
| ssh#248 | 连接自动启用已保存端口映射 | 未处理 | ✅ 已实施：会话建立（含恢复/重连）后自动逐条启用，跳过已跑/冲突，计数提示；随 0.7.11-beta.2 |
| ssh#245 | sz 下载完成自动打开目录 | 未处理 | ✅ 已实施：sz 收完自动 local/reveal 定位落盘目录（能力缺失静默跳过）；随 0.7.11-beta.2 |
| ssh#247 | claude code 终端无法输入 | 未处理（有 WebGL workaround） | 调查结论：输入链路（terminalInputQueue/ghostAcceptKey/按键派发）无任何渲染器分支，盲改无据；已回复征集复现信息（版本/系统/GPU），等用户提供后定向排查 |
| files#91 | S3 无法多选 | 未处理 | ✅ 表头三态全选复选框已实施（随 0.1.92-beta.7）+ 回复澄清版本/现象 |
| ssh#237 | SFTP 传输速度（内网大文件） | 有回复待跟进 | 并发多流二期已排期，本轮不动，跟踪 |
| ssh#209 | MFA 后 SFTP 面板目录不跟随 | 有回复待跟进 | 用户新反馈待回应，P2 评估 |
| ssh#236 | 预览编辑器选区错位 | 有回复待跟进 | 等用户补缩放信息 |
| ssh#241 | MCP 尺寸设置保存不生效 | 已修复（0.7.10） | 待用户确认后关闭 |
| ssh#215 | 空格密码保存置灰 | 有回复待跟进 | 阻塞在宿主仓，插件侧不动（规约 8） |
| ssh#108 | 阿里云堡垒机不兼容 | 有回复待跟进 | Ctrl+F 已修；目标机自选/exit 回列为设计级，规划中 |
| files#92 | FTP 无法下载文件夹 | **未处理（bug）** | P1：archiveDownload FTP 路径打通，随 0.1.92-beta.6 |
| files#91 | S3 无法多选 | 未处理 | ✅ 表头三态全选复选框已实施（随 0.1.92-beta.7）；FileTable 原有复选框/Ctrl+A/shift 区段（0.1.89+），用户现象疑似旧版本，回复已澄清版本/现象 |
| files#90 | 分栏/metadata | 有回复待跟进 | 分栏已修（beta.4）；metadata 为后续 feature，立项跟踪 |
| files#93 | 上传文件权限设置 | 未处理（正文空白） | 先评论澄清场景，不排期 |
| files#85 | sftp 连接不上 | 有回复待跟进 | 0.1.91 已修，等用户回传诊断文本 |

## 本轮执行范围（P0/P1）

1. **ssh#246（安全）**：核查 sidecar MCP 写工具是否全部过只读策略门禁，漏接处补齐 +
   单测；涉及协议变更则同步协议文档。
2. **ssh#243**：预览容器滚动条样式修复 + 前端单测。
3. **files#92**：定位 FTP 协议下 archiveDownload 不通的原因，打通 + smoke。
4. **files#91**：S3/文件列表多选（shift/ctrl/全选）+ 前端单测。
5. 全部改动过各插件 `scripts/test.sh` 全绿后，随 beta 预发布
   （ssh → 0.7.11-beta.1；files → 0.1.92-beta.6；ldap/kafka 无 issue 无改动，
   维持当前 beta 基线不重发）。

## 测试基线（2026-10-28）

- ssh：`scripts/test.sh` → 见 test-ssh.log（首轮 conpty 环境断言失败，已修；后端 1289 单测全绿）
- files：`scripts/test.sh` → 见 test-files.log（计时型测试并行负载下偶发，隔离复跑通过；#92 修复另跑）
- ldap：`scripts/test.sh` → 见 test-ldap-run2.log（Windows 侧 3 处 POSIX 断言 + smoke 管道 select 已修，run2 全绿 13 PASS/7 SKIP）
- kafka：`scripts/test.sh` → 见 test-kafka-run2.log（Windows 侧 smoke sidecar .exe 解析已修，全绿 13 PASS/6 SKIP）

## Issue 回复草稿（待确认后发布）

- **ssh#246**：已定位并修复两层问题——① 无 connectionId 的内联凭据拨打此前按
  host+port+username 精确匹配，换用户名/端口写法可绕过只读注册，已改为分层
  fail-closed 比对；② `ssh_remove_known_host` 漏在只读写门外，已补上。将随
  0.7.11-beta.1 发布。另请补充：您配只读用的是连接表单"只读"开关还是旧版
  MCP 权限设置？绕过时调用的哪个工具？（若是旧版 `mcp_access` 字段，宿主升级
  后该配置会被忽略，属已知迁移项。）
- **ssh#243**：根因是插件全局隐藏滚动条的样式规则未给预览弹窗豁免，已为
  文本/编辑区、hex、Markdown 渲染容器恢复滚动条，随 0.7.11-beta.1 发布。
- **ssh#241**：0.7.10 已含该修复，麻烦升级确认后关闭。
- **ssh#237**：内网大文件单流瓶颈确认，并发多流（自适应引擎二期）已排期。
- **ssh#209**：beta.3 后按新截图仍有目录不跟随的话请再确认插件版本，继续排查。
- **ssh#236**：仍在等缩放比例/系统信息以定位选区错位。
- **ssh#248 / #245 / #247**：已录入跟进清单；#247 请受影响用户确认 WebGL
  引擎关闭时是否可稳定复现。
- **files#92**：已定位修复，随 0.1.92-beta.6 发布；若仍有问题请附报错文本。
- **files#91**：0.1.89 起列表已支持复选框、Ctrl/Cmd+A 全选、shift 区段与 Ctrl
  点选（#82 修复批次）；请先确认插件版本，并说明 S3 连接下具体哪一步失效
  （勾选框不出现 / Ctrl 点选无反应 / 批量操作菜单缺失），表头全选按钮与鼠标
  框选已列入候选增强。
- **files#90**：分栏问题已随 0.1.92-beta.4 修复；metadata 编辑需求已立项跟进。
- **files#93**：需要澄清场景——是 FTP/SFTP 的 chmod 权限位，还是 S3 ACL/元数据？
- **files#85**：0.1.91 已修代理劫持问题；仍失败请提供新报错中 `last attempt:` 完整文本。
