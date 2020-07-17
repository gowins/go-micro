package grpc

import (
	"github.com/micro/go-micro/errors"
	"google.golang.org/grpc/status"
)

func microError(err error) (bool, error) {
	// 这个错误是否可以忽略
	ignore := false

	// no error
	switch err {
	case nil:
		return ignore, nil
	}

	// micro error
	if v, ok := err.(*errors.Error); ok {
		return ignore, v
	}

	// grpc error
	if s, ok := status.FromError(err); ok {
		errMsg := s.Message()
		if e := errors.Parse(errMsg); e.Code > 0 {
			return ignore, e // actually a micro error
		} else if e.Code == -1 {
			ignore = true // actually a business error
			errMsg = e.Detail
		}
		return ignore, errors.InternalServerError("go.micro.client", errMsg)
	}

	// do nothing
	return ignore, err
}
