package trace

import "github.com/micro/go-micro/client/grpc/internal/tracer"

func init() {
	tracer.TurnOn()
}
