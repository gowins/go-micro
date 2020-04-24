package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func WithKeepaliveParams(opts ...grpc.DialOption) []grpc.DialOption {
	opts = append(
		opts,
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: defaultKeepAliveTime, Timeout: defaultKeepAliveTimeout}),
	)
	return opts
}