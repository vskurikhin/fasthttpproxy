package main

import (
	"net"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/fasthttpproxy"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
)

func main() {
	pool.SetDial(fasthttpproxy.FasthttpHTTPDialerDualStackTimeout("", 30*time.Second))

	s := &fasthttp.Server{
		StreamRequestBody:            true,
		MaxRequestBodySize:           0,
		DisablePreParseMultipartForm: true,
		ReduceMemoryUsage:            true,
		Handler:                      proxy.Handler(),
	}

	ln, _ := net.Listen("tcp", ":8080")
	s.Serve(ln) //nolint:errcheck
}
