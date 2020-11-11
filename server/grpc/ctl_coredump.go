package grpc

import (
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

	for _, profile := range profiles {
		_ = pprof.Lookup(profile).WriteTo(dumper, dumpDebug)
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
