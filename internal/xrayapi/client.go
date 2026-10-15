// Code generated from stats.proto service declarations; DO NOT EDIT.
package command

import (
	"context"
	"google.golang.org/grpc"
)

type StatsServiceClient interface {
	GetStats(context.Context, *GetStatsRequest, ...grpc.CallOption) (*GetStatsResponse, error)
	GetStatsOnline(context.Context, *GetStatsRequest, ...grpc.CallOption) (*GetStatsResponse, error)
	QueryStats(context.Context, *QueryStatsRequest, ...grpc.CallOption) (*QueryStatsResponse, error)
	GetSysStats(context.Context, *SysStatsRequest, ...grpc.CallOption) (*SysStatsResponse, error)
	GetStatsOnlineIpList(context.Context, *GetStatsRequest, ...grpc.CallOption) (*GetStatsOnlineIpListResponse, error)
	GetAllOnlineUsers(context.Context, *GetAllOnlineUsersRequest, ...grpc.CallOption) (*GetAllOnlineUsersResponse, error)
	GetUsersStats(context.Context, *GetUsersStatsRequest, ...grpc.CallOption) (*GetUsersStatsResponse, error)
}
type statsServiceClient struct{ cc grpc.ClientConnInterface }

func NewStatsServiceClient(cc grpc.ClientConnInterface) StatsServiceClient {
	return &statsServiceClient{cc}
}
func (c *statsServiceClient) GetStats(ctx context.Context, in *GetStatsRequest, opts ...grpc.CallOption) (*GetStatsResponse, error) {
	out := new(GetStatsResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/GetStats", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *statsServiceClient) GetStatsOnline(ctx context.Context, in *GetStatsRequest, opts ...grpc.CallOption) (*GetStatsResponse, error) {
	out := new(GetStatsResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/GetStatsOnline", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *statsServiceClient) QueryStats(ctx context.Context, in *QueryStatsRequest, opts ...grpc.CallOption) (*QueryStatsResponse, error) {
	out := new(QueryStatsResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/QueryStats", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *statsServiceClient) GetSysStats(ctx context.Context, in *SysStatsRequest, opts ...grpc.CallOption) (*SysStatsResponse, error) {
	out := new(SysStatsResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/GetSysStats", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *statsServiceClient) GetStatsOnlineIpList(ctx context.Context, in *GetStatsRequest, opts ...grpc.CallOption) (*GetStatsOnlineIpListResponse, error) {
	out := new(GetStatsOnlineIpListResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/GetStatsOnlineIpList", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *statsServiceClient) GetAllOnlineUsers(ctx context.Context, in *GetAllOnlineUsersRequest, opts ...grpc.CallOption) (*GetAllOnlineUsersResponse, error) {
	out := new(GetAllOnlineUsersResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/GetAllOnlineUsers", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *statsServiceClient) GetUsersStats(ctx context.Context, in *GetUsersStatsRequest, opts ...grpc.CallOption) (*GetUsersStatsResponse, error) {
	out := new(GetUsersStatsResponse)
	err := c.cc.Invoke(ctx, "/xray.app.stats.command.StatsService/GetUsersStats", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
