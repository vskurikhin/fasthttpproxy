// Package metrics предоставляет Prometheus-метрики для прокси-сервера.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	BufIOWriterFlushErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fasthttp_proxy_bufio_writer_flush_errors_total",
		Help: "Total number of bufio Writer flush errors",
	})

	ReadErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fasthttp_proxy_read_errors_total",
		Help: "Total number of upstream read errors",
	})

	WriteErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fasthttp_proxy_write_errors_total",
		Help: "Total number of upstream write errors",
	})

	CloseErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fasthttp_proxy_close_errors_total",
		Help: "Total number of connection close errors",
	})

	Upstream5xx = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fasthttp_proxy_upstream_5xx_total",
		Help: "Total number of upstream 5xx responses converted to 502",
	})

	RequestBodyWriteDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fasthttp_proxy_request_body_write_seconds",
		Help:    "Time spent writing request body to upstream",
		Buckets: prometheus.DefBuckets,
	})

	ResponseBodyReadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fasthttp_proxy_response_body_read_seconds",
		Help:    "Time spent reading response body from upstream (including streaming)",
		Buckets: prometheus.DefBuckets,
	})
)
