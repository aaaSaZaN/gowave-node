// Package testutil provides a real gRPC transport with deterministic responses.
package testutil

import (
	"context"
	stats "github.com/aaaSaZaN/gowave-node/internal/xrayapi"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

type RPC func(context.Context, proto.Message) (proto.Message, error)

func XrayServer(t testing.TB, calls map[string]RPC) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	requests := map[string]func() proto.Message{
		"StatsService/GetSysStats":          func() proto.Message { return &stats.SysStatsRequest{} },
		"StatsService/GetStatsOnline":       func() proto.Message { return &stats.GetStatsRequest{} },
		"StatsService/GetStatsOnlineIpList": func() proto.Message { return &stats.GetStatsRequest{} },
		"StatsService/GetUsersStats":        func() proto.Message { return &stats.GetUsersStatsRequest{} },
		"StatsService/QueryStats":           func() proto.Message { return &stats.QueryStatsRequest{} },
		"HandlerService/AlterInbound":       func() proto.Message { return &handler.AlterInboundRequest{} },
	}
	for _, service := range []string{"StatsService", "HandlerService"} {
		full := "xray.app.stats.command." + service
		if service == "HandlerService" {
			full = "xray.app.proxyman.command." + service
		}
		descriptor := grpc.ServiceDesc{ServiceName: full, HandlerType: (*interface{})(nil)}
		for key, constructor := range requests {
			parts := strings.Split(key, "/")
			if parts[0] != service {
				continue
			}
			key, constructor := key, constructor
			descriptor.Methods = append(descriptor.Methods, grpc.MethodDesc{MethodName: parts[1], Handler: func(_ interface{}, ctx context.Context, decode func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
				request := constructor()
				if err := decode(request); err != nil {
					return nil, err
				}
				call, ok := calls[key]
				if !ok {
					return nil, status.Error(codes.Unimplemented, key)
				}
				return call(ctx, request)
			}})
		}
		server.RegisterService(&descriptor, struct{}{})
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close() })
	return path
}
