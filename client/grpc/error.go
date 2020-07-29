package grpc

import (
	"github.com/micro/go-micro/errors"
	"google.golang.org/grpc/status"
)

func microError(err error) (bool, error) {
	// no error
	switch err {
	case nil:
		return false, nil // nil 直接是可忽略的
	}

	// micro error
	if v, ok := err.(*errors.Error); ok {
		return false, v
	}

	// grpc error
	if s, ok := status.FromError(err); ok {
		errMsg := s.Message()
		// ignorable 标识来源到是否包了一层 ignore error
		ignorable, errs := errors.UnwrapIgnorableError(errMsg) // 从错误中得到是否可忽略的标识
		if ignorable {
			// 如果是可忽略的则继续解析刚问的错误，得到真实的错误
			errMsg = errs
		}

		if e := errors.Parse(errMsg); e.Code > 0 {
			return ignorable, e // actually a micro error
		}
		return ignorable, errors.InternalServerError("go.micro.client", errMsg)
	}

	// do nothing
	return false, err
}
