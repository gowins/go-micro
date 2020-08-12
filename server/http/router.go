package httpServer

import (
	"bytes"
	"context"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/micro/go-micro/api/resolver"
	"github.com/micro/go-micro/api/resolver/micro"
	"github.com/micro/go-micro/metadata"
	"github.com/micro/go-micro/server"
	"github.com/micro/go-micro/util/ctx"
)

const defaultContentType = "application/json"

var _resolver = micro.NewResolver(resolver.WithNamespace("wpt.api"), resolver.WithHandler("meta"))

// router 用户连接 grpc-gateway http.Handler 和 echo 的 http.Handler
type router struct {
	opts    *server.Options
	handler http.Handler
}

func (r *router) handle(ctx context.Context, req server.Request, rsp interface{}) error {
	r.handler.ServeHTTP(rsp.(http.ResponseWriter), req.(*httpRequest).req.WithContext(ctx))
	return nil
}

func (r *router) handleErr(err error, cx context.Context, rw http.ResponseWriter, req *http.Request) bool {
	if err == nil {
		return true
	}

	rw.WriteHeader(400)
	_, _ = rw.Write([]byte(err.Error()))
	return false
}

func (r *router) genReq(req *http.Request) (*httpRequest, context.Context, error) {
	// 处理 metadata
	cx := ctx.FromRequest(req)
	header := make(metadata.Metadata)
	if md, ok := metadata.FromContext(cx); ok {
		for k, v := range md {
			key := strings.ToLower(k)
			header[key] = v
		}
	}

	ct := defaultContentType
	if ctype, ok := header["x-content-type"]; ok {
		ct = ctype
	}
	delete(header, "x-content-type")
	cx = metadata.NewContext(cx, header)

	ep, err := _resolver.Resolve(req)
	if err != nil {
		return nil, nil, err
	}

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return nil, nil, err
	}
	req.Body = ioutil.NopCloser(bytes.NewReader(body))

	return &httpRequest{
		service:     ep.Name,
		contentType: ct,
		method:      ep.Method,
		body:        body,
		req:         req,
		header:      header,
	}, cx, nil
}

// ServeHTTP prepare writer for http
func (r *router) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	handler := r.handle
	hdlr := r.opts.HdlrWrappers
	for i := len(hdlr); i > 0; i-- {
		handler = hdlr[i-1](handler)
	}

	request, cx, err := r.genReq(req)
	if !r.handleErr(err, cx, rw, req) {
		return
	}

	if !r.handleErr(handler(cx, request, rw), cx, rw, req) {
		return
	}
}

func newRouter(handler http.Handler, opts *server.Options) *router {
	return &router{
		opts:    opts,
		handler: handler,
	}
}
