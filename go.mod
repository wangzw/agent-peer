module github.com/wangzw/agent-peer

go 1.27

require (
	github.com/PeterSR/claude-code-socket-transport v0.1.1
	github.com/fsnotify/fsnotify v1.10.1
	github.com/modelcontextprotocol/go-sdk v1.8.0
	golang.org/x/net v0.60.0
	golang.org/x/sys v0.48.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.2 // indirect
)

tool (
	google.golang.org/grpc/cmd/protoc-gen-go-grpc
	google.golang.org/protobuf/cmd/protoc-gen-go
)

replace github.com/PeterSR/claude-code-socket-transport => ./third_party/claude-code-socket-transport

replace github.com/fsnotify/fsnotify => ./third_party/fsnotify
