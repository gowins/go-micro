package grpc

import (
	"context"
	"log"
	"net"
	"time"
	"unsafe"

	"google.golang.org/grpc"
)

func WithDefaultDialOptions(opts ...grpc.DialOption) []grpc.DialOption {
	return append(opts, withContextDialer())
}

type Dialer struct {
	dialed       bool
	reconnectCnt int
	createdAt    time.Time
}

func withContextDialer() grpc.DialOption {
	d := &Dialer{}

	dialer := func(ctx context.Context, addr string) (net.Conn, error) {
		if !d.dialed {
			d.dialed = true
			d.createdAt = time.Now()
		}

		log.Printf(
			"[grpc] Dialer - ID: %x, addr: %s, reconnect count: %d, created at: %s\n",
			uintptr(unsafe.Pointer(d)), addr, d.reconnectCnt, d.createdAt.Format(time.RFC3339Nano))

		d.reconnectCnt++
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}

	return grpc.WithContextDialer(dialer)
}
