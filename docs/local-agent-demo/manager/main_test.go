package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/bytedance/Elkeid/agent/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestFixtureExchange(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	defer server.Stop()
	demo := newDemo("http://127.0.0.1:8080/plugin")
	proto.RegisterTransferServer(server, demo)
	proto.RegisterFileExtServer(server, demo)
	go server.Serve(listener)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := proto.NewTransferClient(conn).Transfer(ctx, grpc.UseCompressor("snappy"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&proto.PackagedData{AgentId: "test", Product: "elkeid-agent"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Configs) != 1 || cfg.Configs[0].Name != pluginName || cfg.Configs[0].Sha256 == "" {
		t.Fatalf("unexpected config: %v", cfg)
	}
	cmd, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if cmd.GetTask().GetDataType() != 1050 {
		t.Fatalf("unexpected task: %v", cmd)
	}
	for _, tc := range []struct {
		token, content string
		want           proto.FileUploadResponse_StatusCode
	}{
		{"unissued", fixtureContent, proto.FileUploadResponse_FAILED},
		{cmd.Task.Token, "wrong", proto.FileUploadResponse_FAILED},
		{cmd.Task.Token, fixtureContent, proto.FileUploadResponse_SUCCESS},
	} {
		upload, err := proto.NewFileExtClient(conn).Upload(ctx, grpc.UseCompressor("snappy"))
		if err != nil {
			t.Fatal(err)
		}
		if err := upload.Send(&proto.FileUploadRequest{Token: tc.token, Data: []byte(tc.content)}); err != nil {
			t.Fatal(err)
		}
		result, err := upload.CloseAndRecv()
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != tc.want {
			t.Errorf("status = %v, want %v", result.Status, tc.want)
		}
	}
}
