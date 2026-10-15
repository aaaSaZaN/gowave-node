#!/usr/bin/env python3
"""Generate the small service client alongside protoc's message bindings."""
import pathlib
import re

root = pathlib.Path(__file__).resolve().parents[1]
methods = re.findall(r'rpc (\w+)\((\w+)\) returns \((\w+)\)', (root/'internal/xrayapi/stats.proto').read_text())
code = '// Code generated from stats.proto service declarations; DO NOT EDIT.\npackage command\nimport("context";"google.golang.org/grpc")\n'
code += 'type StatsServiceClient interface {\n'
for method, request, response in methods:
    code += f'{method}(context.Context,*{request},...grpc.CallOption)(*{response},error)\n'
code += '}\ntype statsServiceClient struct{cc grpc.ClientConnInterface}\nfunc NewStatsServiceClient(cc grpc.ClientConnInterface)StatsServiceClient{return &statsServiceClient{cc}}\n'
for method, request, response in methods:
    code += f'func(c *statsServiceClient){method}(ctx context.Context,in *{request},opts ...grpc.CallOption)(*{response},error){{out:=new({response});err:=c.cc.Invoke(ctx,"/xray.app.stats.command.StatsService/{method}",in,out,opts...);if err!=nil{{return nil,err}};return out,nil}}\n'
(root/'internal/xrayapi/client.go').write_text(code)
