package main

import (
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	"github.com/vskurikhin/fasthttpproxy/internal/config"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	upstreams, metricsAddr, proxyAddr := config.ParseFlags()
	return runWithUpstreams(metricsAddr, proxyAddr, upstreams)
}

func runWithUpstreams(metricsAddr, proxyAddr string, upstreams []string) error {
	metricsHandler := fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())
	proxyHandler := proxy.Handler(upstreams)

	log.Printf("metrics listening on %s", metricsAddr)
	go serveMetrics(metricsAddr, metricsHandler)

	log.Printf("proxy listening on %s (upstreams: %v)", proxyAddr, upstreams)
	pool.HTTPDialerTimeout(30 * time.Second)
	s := &fasthttp.Server{
		DisableHeaderNamesNormalizing: true,
		DisablePreParseMultipartForm:  true,
		Handler:                       proxyHandler,
		LogAllErrors:                  true,
		MaxRequestBodySize:            fasthttp.DefaultMaxRequestBodySize,
		ReduceMemoryUsage:             true,
		StreamRequestBody:             true,
	}
	return s.ListenAndServe(proxyAddr)
}

func serveMetrics(addr string, handler fasthttp.RequestHandler) {
	if err := fasthttp.ListenAndServe(addr, handler); err != nil {
		log.Printf("metrics server error: %v", err)
	}
}
