// Package upstream предоставляет тип Upstreams для случайного выбора upstream-сервера.
package upstream

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"strings"

	"github.com/vskurikhin/fasthttpproxy/internal/config"
)

const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
)

// Upstream содержит информацию об upstream-сервере.
type Upstream struct {
	Address   string      // оригинальный адрес (например, "https://example.com:443")
	Scheme    string      // схема ("http" или "https")
	Host      string      // хост ("example.com")
	Port      string      // порт ("443")
	TLSConfig *tls.Config // конфигурация TLS (только для https)
}

// Upstreams хранит список upstream-серверов и обеспечивает случайный выбор.
type Upstreams struct {
	m    map[string]*Upstream
	keys []*Upstream
}

// NewUpstreams создаёт Upstreams из переданного списка адресов.
func NewUpstreams(address []string) *Upstreams {
	u := &Upstreams{
		m:    make(map[string]*Upstream, len(address)),
		keys: make([]*Upstream, len(address)),
	}
	for i, addr := range address {
		parsed, err := ParseAddress(addr)
		if err != nil {
			// Валидация уже прошла в config — panic невозможен.
			// Просто сохраняем адрес как есть для совместимости.
			up := &Upstream{
				Address: addr,
				Scheme:  "http",
			}
			u.m[addr] = up
			u.keys[i] = up
			continue
		}
		u.m[addr] = parsed
		u.keys[i] = parsed
	}
	return u
}

// Random возвращает случайный upstream-сервер. Если список пуст, возвращает false.
func (u *Upstreams) Random() (*Upstream, bool) {
	if len(u.keys) == 0 {
		return nil, false
	}
	return u.keys[rand.Intn(len(u.keys))], true
}

// Append добавляет новый upstream-адрес в список.
func (u *Upstreams) Append(address string) {
	if _, ok := u.m[address]; ok {
		return
	}
	parsed, err := ParseAddress(address)
	if err != nil {
		up := &Upstream{
			Address: address,
			Scheme:  SchemeHTTP,
		}
		u.m[address] = up
		u.keys = append(u.keys, up)
		return
	}
	u.m[address] = parsed
	u.keys = append(u.keys, parsed)
}

// ParseAddress парсит адрес upstream и возвращает структуру Upstream.
// addr может быть в формате "http://host:port", "https://host:port" или "host:port".
// Если схема не указана, используется "http".
func ParseAddress(addr string) (*Upstream, error) {
	// Добавляем схему по умолчанию, если её нет
	if !strings.HasPrefix(addr, config.PrefixHTTP) && !strings.HasPrefix(addr, config.PrefixHTTPS) {
		addr = config.PrefixHTTP + addr
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream address %q: %w", addr, err)
	}

	scheme := u.Scheme
	host := u.Hostname()
	port := u.Port()

	if port == "" {
		switch scheme {
		case SchemeHTTPS:
			port = "443"
		default:
			port = "80"
		}
	}

	return &Upstream{
		Address: addr,
		Scheme:  scheme,
		Host:    host,
		Port:    port,
	}, nil
}

// NewTLSConfig создаёт tls.Config из параметров конфигурации.
func NewTLSConfig(values config.Values) (*tls.Config, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: values.TLSInsecureSkipVerify,
	}

	if values.TLSServerName != "" {
		cfg.ServerName = values.TLSServerName
	}

	if values.TLSCAFile != "" {
		caCert, err := os.ReadFile(values.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file %q: %w", values.TLSCAFile, err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %q", values.TLSCAFile)
		}
		cfg.RootCAs = caCertPool
	}

	return cfg, nil
}
