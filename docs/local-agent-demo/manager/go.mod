module elkeid-local-demo-manager

go 1.22

require (
	github.com/bytedance/Elkeid/agent v0.0.0
	google.golang.org/grpc v1.69.4
)

require (
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.19.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241015192408-796eee8c2d53 // indirect
	google.golang.org/protobuf v1.35.1 // indirect
)

replace github.com/bytedance/Elkeid/agent => ../../../agent
