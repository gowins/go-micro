package httpServer

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/micro/go-micro/api/resolver"
	"github.com/micro/go-micro/api/resolver/micro"
	"github.com/micro/go-micro/metadata"
	"github.com/micro/go-micro/server"
)

const defaultContentType = "application/json"
const contentType = "x-content-type"

var _resolver = micro.NewResolver(resolver.WithHandler("meta"))

// router 实现了http.Handler, 同时能够处理HdlrWrappers
type router struct {
	opts    *server.Options
	handler http.Handler
}

func (r *router) handle(ctx context.Context, req server.Request, rsp interface{}) error {
	r.handler.ServeHTTP(rsp.(http.ResponseWriter), req.(*httpRequest).req.WithContext(ctx))
	return nil
}

func (r *router) handleErr(err error, rw http.ResponseWriter) bool {
	if err == nil {
		return true
	}

	rw.WriteHeader(400)
	_, _ = rw.Write([]byte(err.Error()))
	return false
}

// genReq convert http.Request to micro.Request
func (r *router) genReq(req *http.Request) (*httpRequest, context.Context, error) {
	// 处理 metadata header
	header := make(metadata.Metadata)
	for k, v := range req.Header {
		header[strings.ToLower(k)] = strings.Join(v, ",")
	}

	ct := defaultContentType
	if tye, ok := header[contentType]; ok {
		ct = tye
	}
	delete(header, contentType)

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
		service:     fmt.Sprintf("%s.%s", r.opts.Name, ep.Name),
		contentType: ct,
		method:      ep.Method,
		body:        body,
		codec:       newRpcCodec(defaultCodecs[defaultContentType]),
		req:         req,
		header:      header,
	}, metadata.NewContext(req.Context(), header), nil
}

// ServeHTTP prepare writer for http
func (r *router) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	handler := r.handle
	hdlr := r.opts.HdlrWrappers
	for i := len(hdlr); i > 0; i-- {
		handler = hdlr[i-1](handler)
	}

	request, cx, err := r.genReq(req)
	if !r.handleErr(err, rw) {
		return
	}

	if !r.handleErr(handler(cx, request, rw), rw) {
		return
	}
}

func newRouter(handler http.Handler, opts *server.Options) *router {
	if handler == nil || opts == nil {
		panic("handler and opts should not be nil")
	}

	return &router{
		opts:    opts,
		handler: handler,
	}
}
