package main

import (
	"log"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
)

func main() {
	pool.HTTPDialerTimeout(30 * time.Second)
	s := &fasthttp.Server{
		DisableHeaderNamesNormalizing: true,
		DisablePreParseMultipartForm:  true,
		Handler:                       proxy.Handler(),
		LogAllErrors:                  true,
		MaxRequestBodySize:            fasthttp.DefaultMaxRequestBodySize,
		ReduceMemoryUsage:             true,
		StreamRequestBody:             true,
	}
	log.Fatal(s.ListenAndServe(":8080"))
}
