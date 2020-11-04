package grpc

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/micro/go-micro/errors"
	"github.com/micro/go-micro/server"
	"github.com/micro/go-micro/util/log"
)

type controller struct {
	g        *grpcServer
	SwitchCh chan struct{}
	Port     string
}

func newCtl(g *grpcServer) *controller {
	return &controller{
		g:        g,
		SwitchCh: make(chan struct{}, 1),
	}
}

func (c *controller) start() error {
	ts, err := net.Listen("tcp", server.DefaultAddress)
	if err != nil {
		return err
	}

	log.Logf("Controller [http] Listening on %s", ts.Addr().String())

	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(errors.New("", "Hello world!", http.StatusOK).Error()))
	})

	http.HandleFunc("/controller/switch-state", func(writer http.ResponseWriter, request *http.Request) {
		var (
			detail string
		)

		select {
		case c.SwitchCh <- struct{}{}:
			detail = "ok"
		default:
			detail = "wait a second"
		}

		_, _ = writer.Write([]byte(errors.New("", detail, http.StatusOK).Error()))
		return
	})

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

func (c *controller) SwitchState() error {
	g := c.g

	prefix := "pause."
	if err := g.Deregister(); err != nil {
		log.Log("[controller] Server deregister error: ", err)
	}

	if strings.HasPrefix(g.opts.Name, prefix) {
		g.opts.Name = strings.TrimPrefix(g.opts.Name, prefix)
	} else {
		g.opts.Name = prefix + g.opts.Name
	}

	if err := g.Register(); err != nil {
		log.Log("[controller] Server register error: ", err)
	}

	return nil
}
