package httpServer

import (
	"net/http"

	"github.com/micro/go-micro/codec"
	"github.com/micro/go-micro/codec/bytes"
)

type httpRequest struct {
	req         *http.Request
	service     string
	method      string
	contentType string
	codec       codec.Codec
	header      map[string]string
	body        []byte
	stream      bool
	payload     interface{}
}

func (r *httpRequest) ContentType() string {
	return r.contentType
}

func (r *httpRequest) Service() string {
	return r.service
}

func (r *httpRequest) Method() string {
	return r.method
}

func (r *httpRequest) Endpoint() string {
	return r.method
}

func (r *httpRequest) Codec() codec.Reader {
	return r.codec
}

func (r *httpRequest) Header() map[string]string {
	return r.header
}

func (r *httpRequest) Read() ([]byte, error) {
	f := &bytes.Frame{}
	if err := r.codec.ReadBody(f); err != nil {
		return nil, err
	}
	return f.Data, nil
}

func (r *httpRequest) Stream() bool {
	return r.stream
}

func (r *httpRequest) Body() interface{} {
	return r.payload
}
