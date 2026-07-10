package main

import (
	"log"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
)

func main() {
	metricsHandler := fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())
	proxyHandler := proxy.Handler()

	log.Println("metrics listening on :7070")
	go func() {
		log.Fatal(fasthttp.ListenAndServe(":7070", metricsHandler))
	}()

	log.Println("proxy listening on :8080")
	log.Fatal(fasthttp.ListenAndServe(":8080", proxyHandler))
}
