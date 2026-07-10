// Package config предоставляет функции конфигурации для прокси-сервера.
package config

import (
	"flag"
	"log"
	"net/url"
	"os"
	"strings"
)

// ParseFlags парсит флаги из os.Args[1:] и возвращает upstreams, metricsAddr, proxyAddr.
func ParseFlags() (upstreams []string, metricsAddr, proxyAddr string) {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	upstreamsFlag := fs.String("upstreams", "", "Comma-separated list of upstream servers (host:port)")
	metricsAddrFlag := fs.String("metrics-addr", ":7070", "Metrics server listen address")
	proxyAddrFlag := fs.String("proxy-addr", ":8080", "Proxy server listen address")
	_ = fs.Parse(os.Args[1:])

	if *upstreamsFlag != "" {
		upstreams = parseUpstreams(*upstreamsFlag, nil)
	}
	return upstreams, *metricsAddrFlag, *proxyAddrFlag
}

// ParseUpstreams возвращает список upstream-адресов из флага --upstreams (для совместимости).
func ParseUpstreams() []string {
	upstreams, _, _ := ParseFlags()
	return upstreams
}

func parseUpstreams(raw string, upstreams []string) []string {
	for _, addr := range strings.Split(raw, ",") {
		upstreams = addrAppend(upstreams, addr)
	}
	return upstreams
}

func addrAppend(upstreams []string, addr string) []string {
	addr = strings.TrimSpace(addr)
	if _, err := url.Parse("https://" + addr); err != nil {
		log.Fatalf("invalid upstream address: %s", addr)
	}
	upstreams = append(upstreams, addr)
	return upstreams
}
