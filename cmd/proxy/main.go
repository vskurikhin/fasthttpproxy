package main

import (
	"crypto/tls"
	"fmt"
	"log"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	"github.com/vskurikhin/fasthttpproxy/internal/config"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
	"github.com/vskurikhin/fasthttpproxy/internal/upstream"
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
	pool.BufferSize(values.IOBuffersSize)
	pool.HTTPDialerTimeout(values.DialerTimeout)
	pool.IdleTimeout(values.IdleTimeout)
	pool.MaxConnectionsPerHost(values.MaxConnections)
	pool.PipeCopyBufferSize(values.CopyBuffersSize)

	// Установить TLS конфигурацию
	if values.TLSEnabled {
		tlsCfg, err := upstream.NewTLSConfig(values)
		if err != nil {
			return fmt.Errorf("failed to create TLS config: %w", err)
		}
		pool.TLSConfig(tlsCfg)
	}

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
	// Установить TLS конфигурацию
	if values.TLSEnabled {
		if tlsConfig, ok := constructTLSConfig(values); ok {
			s.TLSConfig = tlsConfig
		}
	}
	return s.ListenAndServe(values.ProxyAddr)
}

func serveMetrics(addr string, handler fasthttp.RequestHandler) {
	if err := fasthttp.ListenAndServe(addr, handler); err != nil {
		log.Printf("metrics server error: %v", err)
	}
}

// constructTLSConfig Создаём конфигурацию TLS.
// MinVersion: tls.VersionTLS12, // Требовать TLS 1.2.
func constructTLSConfig(values config.Values) (*tls.Config, bool) {
	result := &tls.Config{}
	if values.TLSServerName == "" {
		return result, false
	}
	if values.TLSServerCertificatePemFile == "" || values.TLSServerKeyPemFile == "" {
		return result, false
	}

	var err error
	result.Certificates = make([]tls.Certificate, 1)
	result.Certificates[0], err = tls.LoadX509KeyPair(values.TLSServerCertificatePemFile, values.TLSServerKeyPemFile)
	if err != nil {
		log.Printf("proxy server start in HTTP! because tls server error: %v", err)
		return result, false
	}

	result.ServerName = values.TLSServerName
	result.InsecureSkipVerify = values.TLSInsecureSkipVerify
	result.MinVersion = tls.VersionTLS12

	return result, true
}
