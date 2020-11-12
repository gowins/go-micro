package grpc

import (
	"net/http"
	"strings"

	"github.com/micro/go-micro/util/log"
)

const (
	Pause = iota
	Resume
)

type Event struct {
	Type int
	Data interface{}
}

func doPause(g *grpcServer) error {
	return pauseOrResume(g, Pause)
}

func doResume(g *grpcServer) error {
	return pauseOrResume(g, Resume)
}

func pauseOrResume(g *grpcServer, eventType int) error {
	prefix := "pause."
	hasPrefix := strings.HasPrefix(g.opts.Name, prefix)

	if (eventType == Pause && hasPrefix) || (eventType == Resume && !hasPrefix) {
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

func pauseHandler(ch chan<- *Event) (pattern string, handler func(http.ResponseWriter, *http.Request)) {
	return "server-pause", func(writer http.ResponseWriter, _ *http.Request) {
		triggerEvent(ch, Pause, writer)
		return
	}
}

func resumeHandler(ch chan<- *Event) (pattern string, handler func(http.ResponseWriter, *http.Request)) {
	return "server-resume", func(writer http.ResponseWriter, _ *http.Request) {
		triggerEvent(ch, Resume, writer)
	}
}

func triggerEvent(ch chan<- *Event, eventType int, writer http.ResponseWriter) {
	var detail string

	select {
	case ch <- &Event{Type: eventType}:
		detail = OK
	default:
		detail = "wait a second"
	}

	Success(writer, detail)
}

func registerEventsHandler(mux *http.ServeMux, ch chan<- *Event) {
	pattern, handler := pauseHandler(ch)
	handleFunc(mux, pattern, handler)

	pattern, handler = resumeHandler(ch)
	handleFunc(mux, pattern, handler)
}
