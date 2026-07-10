package main

import (
	"log"

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
	return runWith(config.ParseFlags())
}

func runWith(values config.Values) error {
	pool.HTTPDialerTimeout(values.DialerTimeout)
	pool.IdleTimeout(values.IdleTimeout)
	pool.MaxConnectionsPerHost(values.MaxConnections)
	metricsHandler := fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())
	proxyHandler := proxy.Handler(values.Upstreams)

	log.Printf("metrics listening on %s", values.MetricsAddr)
	go serveMetrics(values.MetricsAddr, metricsHandler)

	log.Printf(
		"proxy listening on %s (concurrency: %d, dialerTimeout: %v, idleTimeout: %v,"+
			" maxBodySize: %d, maxConnections: %d, upstreams: %v)",
		values.ProxyAddr, values.Concurrency, values.DialerTimeout, values.IdleTimeout,
		values.MaxRequestBodySize, values.MaxConnections, values.Upstreams,
	)
	s := &fasthttp.Server{
		Concurrency:                   values.Concurrency,
		DisableHeaderNamesNormalizing: values.DisableHeaderNamesNormalizing,
		DisableKeepalive:              values.DisableKeepalive,
		DisablePreParseMultipartForm:  values.DisablePreParseMultipartForm,
		GetOnly:                       values.GetOnly,
		Handler:                       proxyHandler,
		LogAllErrors:                  values.LogAllErrors,
		MaxConnsPerIP:                 values.MaxConnectionsPerIP,
		MaxRequestBodySize:            values.MaxRequestBodySize,
		MaxRequestsPerConn:            values.MaxRequestsPerConn,
		NoDefaultContentType:          values.NoDefaultContentType,
		NoDefaultDate:                 values.NoDefaultDate,
		NoDefaultServerHeader:         values.NoDefaultServerHeader,
		ReadBufferSize:                values.ReadBufferSize,
		ReadTimeout:                   values.ReadTimeout,
		ReduceMemoryUsage:             values.ReduceMemoryUsage,
		SecureErrorLogMessage:         values.SecureErrorLogMessage,
		StreamRequestBody:             true,
		WriteBufferSize:               values.WriteBufferSize,
		WriteTimeout:                  values.WriteTimeout,
	}
	return s.ListenAndServe(values.ProxyAddr)
}

func serveMetrics(addr string, handler fasthttp.RequestHandler) {
	if err := fasthttp.ListenAndServe(addr, handler); err != nil {
		log.Printf("metrics server error: %v", err)
	}
}
