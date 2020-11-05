package grpc

import (
	"bytes"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/micro/go-micro/errors"
	"github.com/micro/go-micro/server"
	"github.com/micro/go-micro/util/log"
)

type State int

const (
	Pause State = iota
	Resume
)

type controller struct {
	g        *grpcServer
	SwitchCh chan State
	Port     string
}

func newCtl(g *grpcServer) *controller {
	return &controller{
		g:        g,
		SwitchCh: make(chan State),
	}
}

func (c *controller) start() error {
	ts, err := net.Listen("tcp", server.DefaultAddress)
	if err != nil {
		return err
	}

	log.Logf("Controller [http] Listening on %s", ts.Addr().String())

	if err := c.RegisterHandler(); err != nil {
		return err
	}

	go func() {
		if err := http.Serve(ts, nil); err != nil {
			log.Log("[controller] Http ListenAndServe error: ", err)
		}
	}()

	parts := strings.Split(ts.Addr().String(), ":")
	if len(parts) > 1 {
		port, _ := strconv.Atoi(parts[len(parts)-1])
		c.Port = strconv.Itoa(port)
	}

	return nil
}

func (c *controller) SwitchState(state State) error {
	g := c.g
	prefix := "pause."
	hasPrefix := strings.HasPrefix(g.opts.Name, prefix)

	if (state == Pause && hasPrefix) || (state == Resume && !hasPrefix) {
		return nil
	}

	if err := g.Deregister(); err != nil {
		log.Log("[controller] Server deregister error: ", err)
	}

	if hasPrefix {
		g.opts.Name = strings.TrimPrefix(g.opts.Name, prefix)
	} else {
		g.opts.Name = prefix + g.opts.Name
	}

	if err := g.Register(); err != nil {
		log.Log("[controller] Server register error: ", err)
	}

	return nil
}

func (c *controller) RegisterHandler() error {
	http.HandleFunc(pauseHandler(c.SwitchCh))
	http.HandleFunc(resumeHandler(c.SwitchCh))
	return nil
}

func WrapHandler(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		body, err := ioutil.ReadAll(request.Body)
		if err != nil {
			_, _ = writer.Write(
				[]byte(errors.New("", "ioutil.ReadAll(request.Body) error", http.StatusBadRequest).Error()))
			return
		}

		log.Logf("[controller] URI: %s, Method: %s, RemoteAddr: %s, Header: %v, Body: %v",
			request.RequestURI, request.Method, request.RemoteAddr, request.Header, request.Body)

		request.Body = ioutil.NopCloser(bytes.NewBuffer(body))
		handler(writer, request)
	}
}

func pauseHandler(ch chan<- State) (pattern string, handler func(http.ResponseWriter, *http.Request)) {
	return "/controller/server-pause", WrapHandler(func(writer http.ResponseWriter, _ *http.Request) {
		swtichStateHandler(ch, Pause, writer)
		return
	})
}

func resumeHandler(ch chan<- State) (pattern string, handler func(http.ResponseWriter, *http.Request)) {
	return "/controller/server-resume", WrapHandler(func(writer http.ResponseWriter, _ *http.Request) {
		swtichStateHandler(ch, Resume, writer)
	})
}

func swtichStateHandler(ch chan<- State, state State, writer http.ResponseWriter) {
	var detail string

	select {
	case ch <- state:
		detail = "ok"
	default:
		detail = "wait a second"
	}

	_, _ = writer.Write([]byte(errors.New("", detail, http.StatusOK).Error()))
}
