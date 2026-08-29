# L-D 路进度记录（集成冒烟实跑 / 代码收尾）

> 记录人：L-D（dbx-ldap-plugin 集成冒烟实跑与收尾）。
> 日期：2026-08-28。工具链：go 1.27.0（darwin/arm64）/ dbx-plugin CLI 0.1.0（经 /tmp/go-shim 剥 GOWORK）/ Docker 29.4.0 / python3.11。
> 凭据红线：OpenLDAP 管理员密码全程仅经环境变量（`LDAP_ADMIN_PASSWORD` → compose 变量替换 → 容器 env → `LDAP_TEST_BINDPW`），随机生成、未写入任何文件/日志/报告。

## 1. 任务 1：gofmt + go test 复核

- `gofmt -w backend/internal/ldapconn/policy_test.go`：diff 确认仅 struct 字段对齐空白变化（3 处），无任何语义改动。gofmt -l backend 此后为空。
- `go test -count=1 ./...`：ldapconn / lifecycle / store 三包全绿。

## 2. 任务 2：容器集成冒烟（S1-S10 实跑）

### 2.1 容器配置（本路新增）

| 文件 | 说明 |
|---|---|
| `docker-compose.ldap-test.yml` | bitnami/openldap:latest，端口 1389:1389；`LDAP_ROOT=dc=example,dc=org`；密码 `${LDAP_ADMIN_PASSWORD:?...}` 强制走环境变量（文件内零凭据字面量）；`LDAP_CUSTOM_LDIF_DIR=/ldifs` 挂载种子 |
| `scripts/ldap-seed/01-testdata.ldif` | 自 tiny-rdm ldap-seed 改造：根条目 + ou=People + jane/bob + S7 用的 dummy userPassword 条目（值标注非真实凭据） |
| `scripts/smoke_container.py` | 编排器：compose up → 就绪探测 → 跑 smoke_test.py → 默认 down -v（`--keep` 调试）；用法 `LDAP_ADMIN_PASSWORD=... python3 scripts/smoke_container.py` |

### 2.2 容器侧踩坑（bitnami 配方与 tiny-rdm osixia 配方的差异）

1. **自定义 LDIF 时默认树不导入**：bitnami 在 `LDAP_CUSTOM_LDIF_DIR` 非空时跳过 `ldap_create_tree`，基座 `dc=example,dc=org` 必须由种子自带，否则导入报 `No such object (32)` 且容器 exit 123（表现为端口通但 bind 被重置）。
2. **大小写等价 DN 冲突**：初版种子的 `ou=Groups` 与（彼时默认树遗留）`ou=groups` 等价，`ldapadd`（无 -c）失败即整体退出；种子已收敛为最小集。
3. **就绪探测不能只查端口**：bootstrap 期间会 stop/restart slapd，端口已接受连接但 bind 被 reset。`smoke_container.wait_ready` 改为轮询容器内 `ldapsearch`（admin bind 成功才放行）。

### 2.3 smoke 客户端缺口（scripts/** 修复，backend 零改动）

1. **connection.host 必须是完整 URL**：manifest `url` 字段 binding 为 `host`（`ldap://host:port` 形态），smoke 原用裸 IP 导致 `unsupported ldap url scheme ""`。`make_connection` 改为 `ldap://HOST:PORT`；`sidecar_client_jsonl.lifecycle_params` 相应从 URL hostname 派生 runtime 裸拨号端点（对齐 DBX 宿主：URL 管逻辑主机/SNI，runtime 管 D5 拨号）。
2. **领域方法缺 connectionId**：backend `main.go decodeParams` 要求领域方法必填 `connectionId`（宿主工作台注入），smoke 原未带。新增 `domain()` 包装统一注入（跟踪最近 connect 的连接，S9 断开后注入旧 id 恰好覆盖该场景）。
3. **PASS 结果从不落表**：`@scenario` 装饰器成功路径无 append，全绿时报告 total=0（此前全 SKIP/全 FAIL 掩盖）。补 `else: PASS` 分支。
4. **S7 期望与契约不符**：原 S7 期望"带 userPassword 的 add 成功后读剥离"，但 §5.2 契约与 tiny-rdm 原版（`ensureLDAPWriteAllowed` :1886 原样移植）均为**写请求含屏蔽属性即拒绝**（读才剥离）。S7 重写为双断言：(a) 带 userPassword 的 add 被拒（-32000、文案含 blocked）；(b) 种子 dummy userPassword 条目经 get/search 均不泄露。**backend 未改**。

### 2.4 S1-S10 逐项结果（两轮实跑：默认模式 + LDAP_TEST_REQUIRE=1，均 10/10）

| # | 场景 | 结果 | 备注 |
|---|---|---|---|
| S1 | initialize + connection/test (simple) | **PASS** | simple bind + base 探测返回 1 条 |
| S2 | connect + base scope root 搜索 | **PASS** | entries 非空 |
| S3 | sub + pageSize 分页聚合 = 全量 | **PASS** | pageSize=2 与 1000 聚合计数一致 |
| S4 | add/modify/get/delete 往返 | **PASS** | description 读回 = "after"，删除后 get 报错 |
| S5 | modifyDn 改 RDN | **PASS** | 新 DN 可查、旧 DN 消失 |
| S6 | read_only 写拒绝 | **PASS** | -32000 |
| S7 | 屏蔽属性 | **PASS** | 写拒（blocked）+ 读剥离（get/search 均无 userPassword） |
| S8 | allowed_base_dns 白名单外读拒绝 | **PASS** | 文案 "outside allowed base DNs" |
| S10 | 非法 filter (RFC 4515) 拒绝 | **PASS** | 文案含 filter |
| S9 | disconnect 后再调 | **PASS** | 连接不存在业务错误 |

每轮结束容器均 `down -v` 清理，无残留容器/网络。

### 2.5 打包态闭环

`build.sh` 产出的 `.dbxp` 内二进制由 dbx-plugin CLI 经 go-shim 现场编译（与本地 `backend/bin` 构建参数不同，sha256 差异属预期）。已解包取出包内二进制作为 `DBX_PLUGIN_SIDECAR` 实跑一轮：**10/10 PASS**。

## 3. 任务 3：打包复核

- `PATH="/tmp/go-shim:$PATH" bash scripts/build.sh` 全程绿：前端 typecheck + 68/68 vitest + vite 构建 → ui/index.html（自包含）→ backend go build（-trimpath -s -w）→ `dbx-plugin package .`。
- 产物：`dist/io.dbx.ldap-0.1.0-darwin-arm64.dbxp`（5,146,460 B，sha256 9c7622…，unsigned review candidate）。包内容核验：manifest.json + bin/darwin-arm64/dbx-plugin-ldap + ui/index.html + assets + checksums.json，与 artifact.json 记录一致。

## 4. 改动清单（本路）

| 文件 | 改动 |
|---|---|
| `backend/internal/ldapconn/policy_test.go` | 仅 gofmt（对齐空白），go test 复绿 |
| `scripts/smoke_test.py` | host→URL 形态、`domain()` 注入 connectionId、S7 双断言重写、装饰器补 PASS append |
| `scripts/sidecar_client_jsonl.py` | `lifecycle_params` runtime 端点自 URL hostname 派生 |
| `scripts/smoke_container.py` | 新增（容器编排 + 就绪探测 + 收尾） |
| `scripts/ldap-seed/01-testdata.ldif` | 新增（基座条目 + 种子数据 + S7 fixture） |
| `docker-compose.ldap-test.yml` | 新增（密码仅经环境变量） |
| `backend/bin/dbx-plugin-ldap`、`dist/*.dbxp` | 重建/重打包（产物） |

**backend 业务代码零改动**（冒烟暴露的问题全部为 smoke 客户端/容器配方缺口或契约语义确认，无后端缺陷）。

## 5. 遗留

1. **宿主端到端**（沿袭 PROGRESS-C §5.2）：.dbxp 未安装到测试宿主验证连接表单与工作台；S1 实跑已证明 `url 绑定 host` 形态（connection.host = `ldap://…`）在 sidecar 侧成立，宿主侧只需照 manifest 发 lifecycle params。
2. **浏览器截图验证**（L1-8 DoD）：仍需宿主或 mock 桥环境。
3. **TLS 路径**：冒烟为明文 ldap://；ldaps/StartTLS 未在本轮容器覆盖（bitnami 需另配证书），后端单测已覆盖 TLS 配置逻辑。
4. **ldapi / M3 认证**（kerberos/ntlm/digest_md5/external）：dial 层显式报 "not supported yet (planned for M3)"，符合预期，未在 smoke 范围。

## 6. 阻塞

无。S1-S10 容器实跑全绿，打包复核完成，容器与临时产物已清理。
