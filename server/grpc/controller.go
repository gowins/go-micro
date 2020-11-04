package grpc

import (
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

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
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)

	go func() {
		// wait on switch signal
		for range ch {
			c.SwitchCh <- struct{}{}
		}
	}()

	ts, err := net.Listen("tcp", server.DefaultAddress)
	if err != nil {
		return err
	}

	log.Logf("Controller [http] Listening on %s", ts.Addr().String())

	//http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
	//	writer.Write([]byte("Hello world!"))
	//})

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
