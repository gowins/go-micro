// Package http implements a go-micro.Server
package httpServer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/micro/go-micro"
	"github.com/micro/go-micro/broker"
	"github.com/micro/go-micro/codec"
	"github.com/micro/go-micro/codec/jsonrpc"
	"github.com/micro/go-micro/codec/protorpc"
	"github.com/micro/go-micro/config/cmd"
	"github.com/micro/go-micro/registry"
	"github.com/micro/go-micro/server"
	"github.com/micro/go-micro/util/log"
)

var (
	defaultCodecs = map[string]codec.NewCodec{
		"application/json":         jsonrpc.NewCodec,
		"application/json-rpc":     jsonrpc.NewCodec,
		"application/protobuf":     protorpc.NewCodec,
		"application/proto-rpc":    protorpc.NewCodec,
		"application/octet-stream": protorpc.NewCodec,
	}
)

type httpServer struct {
	sync.Mutex
	ln           net.Listener
	server       *http.Server
	opts         server.Options
	hd           server.Handler
	exit         chan chan error
	registerOnce sync.Once
	subscribers  map[*httpSubscriber][]broker.Subscriber
	// used for first registration
	registered bool
}

func init() {
	cmd.DefaultServers["http"] = NewServer
}

func (e *httpServer) newCodec(contentType string) (codec.NewCodec, error) {
	if cf, ok := e.opts.Codecs[contentType]; ok {
		return cf, nil
	}
	if cf, ok := defaultCodecs[contentType]; ok {
		return cf, nil
	}
	return nil, fmt.Errorf("unsupported Content-Type: %s", contentType)
}

func (e *httpServer) Options() server.Options {
	e.Lock()
	opts := e.opts
	e.Unlock()
	return opts
}

func (e *httpServer) Init(opts ...server.Option) error {
	e.Lock()
	for _, o := range opts {
		o(&e.opts)
	}
	e.Unlock()
	return nil
}

func (e *httpServer) Handle(handler server.Handler) error {
	if _, ok := handler.Handler().(http.Handler); !ok {
		return errors.New("Handle requires http.Handler")
	}
	e.Lock()
	e.hd = handler
	e.Unlock()
	return nil
}

func (e *httpServer) NewHandler(handler interface{}, opts ...server.HandlerOption) server.Handler {
	options := server.HandlerOptions{
		Metadata: make(map[string]map[string]string),
	}

	for _, o := range opts {
		o(&options)
	}

	var eps []*registry.Endpoint

	if !options.Internal {
		for name, metadata := range options.Metadata {
			eps = append(eps, &registry.Endpoint{
				Name:     name,
				Metadata: metadata,
			})
		}
	}

	return &httpHandler{
		eps:  eps,
		hd:   handler,
		opts: options,
	}
}

func (e *httpServer) NewSubscriber(topic string, handler interface{}, opts ...server.SubscriberOption) server.Subscriber {
	return newSubscriber(topic, handler, opts...)
}

func (e *httpServer) Subscribe(sb server.Subscriber) error {
	sub, ok := sb.(*httpSubscriber)
	if !ok {
		return fmt.Errorf("invalid subscriber: expected *httpSubscriber")
	}
	if len(sub.handlers) == 0 {
		return fmt.Errorf("invalid subscriber: no handler functions")
	}

	if err := validateSubscriber(sb); err != nil {
		return err
	}

	e.Lock()
	defer e.Unlock()
	_, ok = e.subscribers[sub]
	if ok {
		return fmt.Errorf("subscriber %v already exists", e)
	}
	e.subscribers[sub] = nil
	return nil
}

func (e *httpServer) Register() error {
	e.Lock()
	opts := e.opts
	eps := e.hd.Endpoints()
	e.Unlock()

	service := serviceDef(opts)
	service.Endpoints = eps

	e.Lock()
	var subscriberList []*httpSubscriber
	for e := range e.subscribers {
		// Only advertise non internal subscribers
		if !e.Options().Internal {
			subscriberList = append(subscriberList, e)
		}
	}
	sort.Slice(subscriberList, func(i, j int) bool {
		return subscriberList[i].topic > subscriberList[j].topic
	})
	for _, e := range subscriberList {
		service.Endpoints = append(service.Endpoints, e.Endpoints()...)
	}
	e.Unlock()

	rOpts := []registry.RegisterOption{
		registry.RegisterTTL(opts.RegisterTTL),
	}

	e.registerOnce.Do(func() {
		log.Logf("Registering node: %s", opts.Name+"-"+opts.Id)
	})

	if err := opts.Registry.Register(service, rOpts...); err != nil {
		return err
	}

	e.Lock()
	defer e.Unlock()

	if e.registered {
		return nil
	}
	e.registered = true

	for sb, _ := range e.subscribers {
		handler := e.createSubHandler(sb, opts)
		var subOpts []broker.SubscribeOption
		if queue := sb.Options().Queue; len(queue) > 0 {
			subOpts = append(subOpts, broker.Queue(queue))
		}

		if !sb.Options().AutoAck {
			subOpts = append(subOpts, broker.DisableAutoAck())
		}

		sub, err := opts.Broker.Subscribe(sb.Topic(), handler, subOpts...)
		if err != nil {
			return err
		}
		e.subscribers[sb] = []broker.Subscriber{sub}
	}
	return nil
}

func (e *httpServer) Deregister() error {
	e.Lock()
	opts := e.opts
	e.Unlock()

	log.Logf("Deregistering node: %s", opts.Name+"-"+opts.Id)

	service := serviceDef(opts)
	if err := opts.Registry.Deregister(service); err != nil {
		return err
	}

	e.Lock()
	if !e.registered {
		e.Unlock()
		return nil
	}
	e.registered = false

	for sb, subs := range e.subscribers {
		for _, sub := range subs {
			log.Logf("Unsubscribing from topic: %s", sub.Topic())
			sub.Unsubscribe()
		}
		e.subscribers[sb] = nil
	}
	e.Unlock()
	return nil
}

// startListen 开始网关功能监听
func (e *httpServer) startListen() (err error) {
	e.Lock()
	opts := e.opts
	hd := e.hd
	e.Unlock()

	var _ln net.Listener
	if ln, ok := opts.Context.Value(newListener{}).(net.Listener); ok && ln != nil {
		_ln = ln
	} else {
		_ln, err = net.Listen("tcp", opts.Address)
		if err != nil {
			return err
		}
	}

	if _ln == nil {
		return errors.New("net listen error")
	}

	handler, ok := hd.Handler().(http.Handler)
	if !ok {
		return errors.New("Server required http.Handler")
	}

	e.Lock()
	e.ln = _ln
	e.opts.Address = _ln.Addr().String()
	e.server = &http.Server{Handler: handler}
	e.Unlock()

	go func() {
		if err := e.server.Serve(_ln); err != http.ErrServerClosed {
			log.Fatalf("HTTP server: %v, address is :", err, opts.Address)
		}
	}()
	return nil
}

func (e *httpServer) Start() error {
	return e.startListen()
}

func (e *httpServer) Pause() error {
	return nil
}

func (e *httpServer) Resume() error {
	return nil
}

type newListener struct{}

func (e *httpServer) Stop() error {
	if e.server == nil {
		return nil
	}

	// Wait for connections to drain.
	ctx, cancel := context.WithTimeout(e.opts.Context, time.Minute)
	defer cancel()
	return e.server.Shutdown(ctx)
}

func (e *httpServer) String() string {
	return "http"
}

func newServer(opts ...server.Option) server.Server {
	return &httpServer{
		opts: newOptions(opts...),
		//exit:        make(chan chan error),
		subscribers: make(map[*httpSubscriber][]broker.Subscriber),
	}
}

func NewServer(opts ...server.Option) server.Server {
	return newServer(opts...)
}

func WithListener(ln net.Listener) micro.Option {
	return func(o *micro.Options) {
		_ = o.Server.Init(func(opts *server.Options) {
			opts.Context = context.WithValue(opts.Context, newListener{}, ln)
		})
	}
}
