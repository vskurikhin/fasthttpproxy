// Package config предоставляет функции конфигурации для прокси-сервера.
package config

import (
	"flag"
	"log"
	"net/url"
	"os"
	"strings"
	"time"
)

//goland:noinspection GoLinter
type Values struct {
	Concurrency, MaxConnections     int
	IdleTimeout, DialerTimeout      time.Duration
	MaxRequestBodySize              int
	MetricsAddr, ProxyAddr          string
	Upstreams                       []string
	DisableHeaderNamesNormalizing   bool
	DisableKeepalive                bool
	DisablePreParseMultipartForm    bool
	GetOnly, LogAllErrors           bool
	MaxConnectionsPerIP             int
	MaxRequestsPerConn              int
	NoDefaultContentType            bool
	NoDefaultDate                   bool
	NoDefaultServerHeader           bool
	ReadBufferSize, WriteBufferSize int
	ReadTimeout, WriteTimeout       time.Duration
	ReduceMemoryUsage               bool
	SecureErrorLogMessage           bool
}

// ParseFlags парсит флаги из os.Args[1:] и возвращает Values.
// Поддерживаемые флаги:
//
//	--concurrency                  макс. кол-во одновременных запросов к прокси (по умолчанию 262144)
//	--dialer-timeout               таймаут функции устанавливающих tcp-upstream-соединения (по умолчанию 30s)
//	--disable-header-norm          отключить нормализацию заголовков (по умолчанию true)
//	--disable-keepalive            отключить keepalive (по умолчанию false)
//	--disable-preparse-multipart   отключить предварительный парсинг multipart (по умолчанию false)
//	--get-only                     только GET-запросы (по умолчанию false)
//	--idle-timeout                 таймаут бездействия соединения в пуле (по умолчанию 45s, минимум 1s)
//	--log-all-errors               логировать все ошибки (по умолчанию true)
//	--max-body-size                макс. размер тела запроса (по умолчанию 4 МегаБайта)
//	--max-conns                    макс. кол-во соединений на upstream (по умолчанию 100, минимум 1)
//	--max-conns-per-ip             макс. кол-во соединений на IP (по умолчанию 0 = без лимита)
//	--max-reqs-per-conn            макс. кол-во запросов на соединение (по умолчанию 0 = без лимита)
//	--metrics-addr                 адрес metrics-сервера (по умолчанию :7070)
//	--no-default-content-type      не устанавливать Content-Type по умолчанию (по умолчанию false)
//	--no-default-date              не устанавливать Date по умолчанию (по умолчанию false)
//	--no-default-server-header     не устанавливать Server-заголовок (по умолчанию true)
//	--proxy-addr                   адрес proxy-сервера (по умолчанию :8080)
//	--read-buffer-size             размер буфера чтения (по умолчанию 0 = default)
//	--read-timeout                 таймаут чтения (по умолчанию 0 = без лимита)
//	--reduce-memory-usage          режим пониженного потребления памяти (по умолчанию true)
//	--secure-error-log             безопасный лог ошибок (по умолчанию true)
//	--upstreams                    список upstream-серверов через запятую (host:port)
//	--write-buffer-size            размер буфера записи (по умолчанию 0 = по умолчанию в fastHTTP)
//	--write-timeout                таймаут записи (по умолчанию 0 = по умолчанию в fastHTTP)
func ParseFlags() Values {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	concurrencyFlag := fs.Int("concurrency", 256*1024, "Max concurrent requests to proxy (fasthttp.DefaultConcurrency)")
	dialerTimeoutFlag := fs.Duration("dialer-timeout", 30*time.Second, "Dial timeout for upstream connections (default 30s)")
	disableHeaderNormFlag := fs.Bool("disable-header-norm", true, "Disable header names normalizing")
	disableKeepaliveFlag := fs.Bool("disable-keepalive", false, "Disable keepalive")
	disablePreparseMultipartFlag := fs.Bool("disable-preparse-multipart", false, "Disable pre-parse multipart form")
	getOnlyFlag := fs.Bool("get-only", false, "Get only mode")
	idleTimeoutFlag := fs.Duration("idle-timeout", 45*time.Second, "Idle timeout for connections in pool (minimum 1s)")
	logAllErrorsFlag := fs.Bool("log-all-errors", true, "Log all errors")
	maxBodySizeFlag := fs.Int("max-body-size", 4*1024*1024, "Max request body size in bytes (4 MiB)")
	maxConnectionsFlag := fs.Int("max-conns", 100, "Max concurrent connections per upstream (minimum 1)")
	maxConnectionsPerIPFlag := fs.Int("max-conns-per-ip", 0, "Max connections per IP (0 = unlimited)")
	maxRequestsPerConnFlag := fs.Int("max-reqs-per-conn", 0, "Max requests per connection (0 = unlimited)")
	metricsAddrFlag := fs.String("metrics-addr", ":7070", "Metrics server listen address")
	noDefaultContentTypeFlag := fs.Bool("no-default-content-type", false, "No default content type")
	noDefaultDateFlag := fs.Bool("no-default-date", false, "No default date")
	noDefaultServerHeaderFlag := fs.Bool("no-default-server-header", true, "No default server header")
	proxyAddrFlag := fs.String("proxy-addr", ":8080", "Proxy server listen address")
	readBufferSizeFlag := fs.Int("read-buffer-size", 0, "Read buffer size (0 = default in fastHTTP)")
	readTimeoutFlag := fs.Duration("read-timeout", 0, "Read timeout (0 = no timeout)")
	reduceMemoryUsageFlag := fs.Bool("reduce-memory-usage", true, "Reduce memory usage mode")
	secureErrorLogFlag := fs.Bool("secure-error-log", true, "Secure error log message")
	upstreamsFlag := fs.String("upstreams", "", "Comma-separated list of upstream servers (host:port)")
	writeBufferSizeFlag := fs.Int("write-buffer-size", 0, "Write buffer size (0 = default in fastHTTP)")
	writeTimeoutFlag := fs.Duration("write-timeout", 0, "Write timeout (0 = no timeout)")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	var upstreams []string
	if *upstreamsFlag != "" {
		upstreams = parseUpstreams(*upstreamsFlag, nil)
	}
	return Values{
		Concurrency:                   *concurrencyFlag,
		DialerTimeout:                 *dialerTimeoutFlag,
		DisableHeaderNamesNormalizing: *disableHeaderNormFlag,
		DisableKeepalive:              *disableKeepaliveFlag,
		DisablePreParseMultipartForm:  *disablePreparseMultipartFlag,
		GetOnly:                       *getOnlyFlag,
		IdleTimeout:                   *idleTimeoutFlag,
		LogAllErrors:                  *logAllErrorsFlag,
		MaxConnectionsPerIP:           *maxConnectionsPerIPFlag,
		MaxConnections:                *maxConnectionsFlag,
		MaxRequestBodySize:            *maxBodySizeFlag,
		MaxRequestsPerConn:            *maxRequestsPerConnFlag,
		MetricsAddr:                   *metricsAddrFlag,
		NoDefaultContentType:          *noDefaultContentTypeFlag,
		NoDefaultDate:                 *noDefaultDateFlag,
		NoDefaultServerHeader:         *noDefaultServerHeaderFlag,
		ProxyAddr:                     *proxyAddrFlag,
		ReadBufferSize:                *readBufferSizeFlag,
		ReadTimeout:                   *readTimeoutFlag,
		ReduceMemoryUsage:             *reduceMemoryUsageFlag,
		SecureErrorLogMessage:         *secureErrorLogFlag,
		Upstreams:                     upstreams,
		WriteBufferSize:               *writeBufferSizeFlag,
		WriteTimeout:                  *writeTimeoutFlag,
	}
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
