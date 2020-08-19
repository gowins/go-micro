package httpServer

import (
	"context"
	"github.com/micro/go-micro/registry/memory"
	"github.com/micro/go-micro/server"
	"io/ioutil"
	"net/http"
	"testing"
)

func TestHttp(t *testing.T) {
	const addr = "localhost:8080"
	const body = `hello world`

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	})

	r := memory.NewRegistry()
	srv := NewServer(
		server.Name("helloworld"),
		server.Registry(r),
		server.Address(addr),
	)
	if err := srv.Handle(srv.NewHandler(mux)); err != nil {
		panic(err)
	}
	if err := srv.Start(); err != nil {
		panic(err)
	}

	defer func() {
		if err := srv.Stop(); err != nil {
			panic(err)
		}
	}()

	resp, err := http.Get("http://" + addr)
	if err != nil {
		panic(err)
	}
	if resp.StatusCode != http.StatusOK {
		panic("状态码不正确")
	}
	dat, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	if string(dat) != body {
		t.Fatalf("expect %s, got %s", body, dat)
	}

	// Restart
	if err := srv.Init(func(o *server.Options) {
		o.Context = context.WithValue(o.Context, NewListener{}, true)
	}); err != nil {
		panic(err)
	}
}
