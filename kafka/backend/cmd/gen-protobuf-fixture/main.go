// gen-protobuf-fixture：离线生成 PROTOBUF seed fixture（IMPL_PLAN §12.4）。
//
// 本机无 protoc，用 descriptorpb 程序化构造 orders.proto（package
// com.dbx.test，message Order { string id = 1; int64 amount = 2;
// string item = 3; }）的 FileDescriptorSet，proto.Marshal 后输出
// base64（单行 + 换行）到指定文件。参考 .proto 手写件同目录提交：
// kafka/scripts/kafka-seed/protobuf/orders.proto。
//
// 用法：go run ./cmd/gen-protobuf-fixture <输出路径，如
// ../../scripts/kafka-seed/protobuf/orders_fdset.b64>
//
// fixture 提交入库，运行时（seed/smoke/单测）不需要 protoc，也不需要重跑
// 本命令；仅 orders.proto 变更时重新生成。
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen-protobuf-fixture <output.b64>")
		os.Exit(2)
	}
	raw, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{ordersFileDescriptor()},
	})
	if err != nil {
		fail("marshal FileDescriptorSet: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw) + "\n"
	if err := os.MkdirAll(filepath.Dir(os.Args[1]), 0o755); err != nil {
		fail("create output dir: %v", err)
	}
	if err := os.WriteFile(os.Args[1], []byte(encoded), 0o644); err != nil {
		fail("write %s: %v", os.Args[1], err)
	}
	fmt.Printf("wrote %s (%d base64 bytes)\n", os.Args[1], len(encoded)-1)
}

// ordersFileDescriptor 程序化构造 orders.proto 的 FileDescriptorProto
// （与手写参考 orders.proto 逐字段一致）。
func ordersFileDescriptor() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("orders.proto"),
		Package: proto.String("com.dbx.test"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Order"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{
					Name:   proto.String("id"),
					Number: proto.Int32(1),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				},
				{
					Name:   proto.String("amount"),
					Number: proto.Int32(2),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum(),
				},
				{
					Name:   proto.String("item"),
					Number: proto.Int32(3),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				},
			},
		}},
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-protobuf-fixture: "+format+"\n", args...)
	os.Exit(1)
}
