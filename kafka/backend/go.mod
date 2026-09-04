module io.dbx.kafka.plugin

go 1.24.0

require (
	github.com/google/uuid v1.6.0
	github.com/klauspost/compress v1.18.4
	github.com/pierrec/lz4/v4 v4.1.25
	github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk v0.0.0-00010101000000-000000000000
	github.com/twmb/franz-go v1.20.7
	github.com/twmb/franz-go/pkg/kadm v1.17.2
	github.com/twmb/franz-go/pkg/kmsg v1.12.0
)

require golang.org/x/crypto v0.48.0 // indirect

// SDK 不在公网 module proxy 上（unknown revision），本地构建指向宿主 worktree
// 的 SDK 源码（与 CLI npm 包 sdk-root 内容一致，M0 文档 §1.3）。
// `dbx-plugin package` 打包时 CLI 会通过 DBX_PLUGIN_SDK_ROOT + go.work 注入
// 自己的解析路径，此 replace 不影响打包。
replace github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk => ../../../dbx-plugin-host-worktree/plugins/sdk/go/dbx-plugin-sdk
