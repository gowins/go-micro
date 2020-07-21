package grpc

import (
	"github.com/micro/go-micro/errors"
	"google.golang.org/grpc/status"
)

func microError(err error) (bool, error) {
	// 这个错误是否可以忽略
	ignorable := false

	// no error
	switch err {
	case nil:
		return ignorable, nil
	}

	// micro error
	if v, ok := err.(*errors.Error); ok {
		// micro的errors包增加了一个特定的错误类型，
		// 避免一些特殊情况我们没有覆盖到（比如grpc.call之前就返回错误了）。
		if v.Code == errors.StatusIgnorableError {
			ignorable = true // actually a business error
		}
		return ignorable, v
	}

	// grpc error
	if s, ok := status.FromError(err); ok {
		errMsg := s.Message()
		if e := errors.Parse(errMsg); e.Code == errors.StatusIgnorableError {
			ignorable = true // actually a business error
			errMsg = e.Detail
		} else if e.Code > 0 {
			return ignorable, e // actually a micro error
		}
		return ignorable, errors.InternalServerError("go.micro.client", errMsg)
	}

	// do nothing
	return ignorable, err
}
