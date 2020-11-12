package grpc

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/micro/go-micro/errors"
	"github.com/micro/go-micro/server"
	"github.com/micro/go-micro/util/log"
)

const (
	PatternPrefix = "/controller"
	OK            = "ok"
)

type controller struct {
	mux     *http.ServeMux
	Port    string
	EventCh chan *Event
}

func newCtl() *controller {
	return &controller{
		mux:     http.NewServeMux(),
		EventCh: make(chan *Event),
	}
}

func (c *controller) start(ctlHdlrs map[string]func(http.ResponseWriter, *http.Request)) error {
	ln, err := net.Listen("tcp", server.DefaultAddress)
	if err != nil {
		return err
	}

	log.Logf("Controller [http] Listening on %s", ln.Addr().String())

	// Server events
	registerEventsHandler(c.mux, c.EventCh)

	// CoreDump
	registerCoreDumpHandler(c.mux)

	for pattern, handler := range ctlHdlrs {
		handleFunc(c.mux, pattern, handler)
	}

	go func() {
		if err := http.Serve(ln, c.mux); err != nil {
			log.Log("[controller] Http ListenAndServe error: ", err)
		}
	}()

	parts := strings.Split(ln.Addr().String(), ":")
	if len(parts) > 1 {
		port, _ := strconv.Atoi(parts[len(parts)-1])
		c.Port = strconv.Itoa(port)
	}

	return nil
}

func (c *controller) handle(g *grpcServer, event *Event) error {
	switch event.Type {
	case Pause:
		return doPause(g)
	case Resume:
		return doResume(g)
	}
	return nil
}

func handleFunc(mux *http.ServeMux, pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.HandleFunc(getCtlPattern(pattern), wrapHandler(handler))
}

// pattern = "check" => /controller/check
func getCtlPattern(pattern string) string {
	return fmt.Sprintf("%s/%s", PatternPrefix, pattern)
}

func wrapHandler(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				log.Logf("[controller] Panic: %v", r)
			}
		}()

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

func Success(writer http.ResponseWriter, detail string) {
	_, _ = writer.Write([]byte(errors.New("", detail, 0).Error()))
}
