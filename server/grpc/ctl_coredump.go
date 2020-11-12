package grpc

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime/pprof"

	"github.com/micro/go-micro/util/log"
)

var (
	dumpDebug   = 1
	allProfiles = []string{"goroutine", "threadcreate", "heap", "allocs", "block", "mutex"}
)

type dumper struct{}

func newDumper() *dumper {
	return &dumper{}
}

func (*dumper) Write(stack []byte) (n int, err error) {
	log.Log(string(stack))
	return len(stack), nil
}

func doDump(name string) {
	dumper := newDumper()
	profiles := []string{name}

	if name == "all" {
		profiles = allProfiles
	}

	buf := bytes.NewBuffer(nil)
	for _, profile := range profiles {
		buf.Reset()
		_ = pprof.Lookup(profile).WriteTo(buf, dumpDebug)
		_, _ = buf.WriteTo(dumper)
	}
}

func coreDumpHandler(name string) (pattern string, handler func(http.ResponseWriter, *http.Request)) {
	return GetCtlPattern(fmt.Sprintf("%s-dump", name)), WrapHandler(func(writer http.ResponseWriter, _ *http.Request) {
		doDump(name)
		Success(writer, OK)
	})
}

func registerCoreDumpHandler(mux *http.ServeMux) {
	profiles := append(allProfiles, "all")

	for _, profile := range profiles {
		mux.HandleFunc(coreDumpHandler(profile))
	}
}
