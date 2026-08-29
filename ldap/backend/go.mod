module io.dbx.ldap.plugin

go 1.24.0

require (
	github.com/go-ldap/ldap/v3 v3.4.13
	github.com/google/uuid v1.6.0
	github.com/jcmturner/gokrb5/v8 v8.4.4
	github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk v0.0.0-00010101000000-000000000000
)

require (
	github.com/Azure/go-ntlmssp v0.1.0 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.8-0.20250403174932-29230038a667 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/goidentity/v6 v6.0.1 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.50.0 // indirect
)

// SDK 不在公网 module proxy 上（unknown revision），本地构建指向宿主 worktree
// 的 SDK 源码（与 CLI npm 包 sdk-root 内容一致，M0 文档 §1.3）。
// `dbx-plugin package` 打包时 CLI 会通过 DBX_PLUGIN_SDK_ROOT + go.work 注入
// 自己的解析路径，此 replace 不影响打包。
replace github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk => ../../../dbx-plugin-host-worktree/plugins/sdk/go/dbx-plugin-sdk
