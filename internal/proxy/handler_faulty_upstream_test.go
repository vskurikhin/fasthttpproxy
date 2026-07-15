package proxy

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

// generateSelfSignedCert создаёт самоподписанный сертификат и ключ для заданного CN.
// Возвращает сертификат и ключ в PEM-формате, пригодные для tls.X509KeyPair.
func generateSelfSignedCert(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM
}

// startTLSServer запускает TLS-сервер с самоподписанным сертификатом для CN=127.0.0.1.
// После принятия соединения читает запрос и отправляет предопределённый ответ.
func startTLSServer(t *testing.T, response string) net.Listener {
	t.Helper()
	certPEM, keyPEM := generateSelfSignedCert(t, "127.0.0.1")
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("failed to load key pair: %v", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req := fasthttp.AcquireRequest()
				_ = req.Read(br)
				fasthttp.ReleaseRequest(req)
				bw := bufio.NewWriter(c)
				_, _ = bw.WriteString(response)
				_ = bw.Flush()
			}(conn)
		}
	}()
	return ln
}

// startPlainTCPServer запускает обычный TCP-сервер, который отвечает предопределённым ответом.
// Используется для тестов, где прокси пытается подключиться по HTTPS к HTTP-серверу.
func startPlainTCPServer(t *testing.T, response string) (net.Listener, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req := fasthttp.AcquireRequest()
				_ = req.Read(br)
				fasthttp.ReleaseRequest(req)
				bw := bufio.NewWriter(c)
				_, _ = bw.WriteString(response)
				_ = bw.Flush()
			}(conn)
		}
	}()
	return ln, func() { ln.Close() }
}

// FaultType определяет тип сбоя для startFaultyUpstream.
type FaultType int

const (
	FaultNone                                  FaultType = iota
	FaultCloseImmediate                                  // закрыть соединение без отправки данных
	FaultPartialHeaders                                  // отправить частичный заголовок и закрыть
	FaultContentLengthUnderread                          // отправить полный заголовок, часть тела, закрыть
	FaultChunkedDisconnect                               // отправить chunked-ответ без терминатора
	FaultClientDisconnectRequest                         // upstream получает неполное тело запроса (клиент оборвал POST)
	FaultClientDisconnectResponseContentLength           // upstream отправляет полный ответ с Content-Length; прокси начинает стриминг, клиент обрывает
	FaultClientDisconnectResponseChunked                 // upstream отправляет chunked-ответ; прокси начинает стриминг, клиент обрывает
	FaultContentLengthUnderreadZeroBody                  // Content-Length: 100, 0 байт тела, закрытие
	FaultContentLengthUnderread99                        // Content-Length: 100, 99 байт тела, закрытие
)

// startFaultyUpstream запускает TCP-сервер, который симулирует заданный сбой.
// Возвращает listener — адрес доступен через ln.Addr().
func startFaultyUpstream(t *testing.T, fault FaultType) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				switch fault {
				case FaultCloseImmediate:
					// ничего не пишем, просто закрываем
				case FaultPartialHeaders:
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\n")
					_ = bw.Flush()
					// закрываем — заголовок неполный
				case FaultContentLengthUnderread:
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n")
					_, _ = bw.WriteString(strings.Repeat("x", 50))
					_ = bw.Flush()
					// закрываем — тело неполное
				case FaultChunkedDisconnect:
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n")
					_, _ = bw.WriteString("5\r\nhello\r\n")
					_ = bw.Flush()
					// закрываем без 0\r\n\r\n
				case FaultContentLengthUnderreadZeroBody:
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n")
					_ = bw.Flush()
					// закрываем — тело пустое, заголовки говорят 100 байт
				case FaultContentLengthUnderread99:
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n")
					_, _ = bw.WriteString(strings.Repeat("x", 99))
					_ = bw.Flush()
					// закрываем — тело 99 из 100
				}
			}(conn)
		}
	}()
	return ln
}

// startFaultyClientUpstream запускает TCP-сервер, который симулирует поведение upstream
// при обрыве клиента во время стриминга.
//
// Для FaultClientDisconnectRequest: upstream читает частичный запрос и закрывает.
// Для FaultClientDisconnectResponseContentLength: upstream отправляет полный заголовок
// с Content-Length: 1000, затем часть тела (500 байт) — симуляция того, что fasthttp
// перестал читать из-за обрыва клиента.
// Для FaultClientDisconnectResponseChunked: upstream отправляет chunked-заголовок,
// один чанк, и закрывает без терминатора.
func startFaultyClientUpstream(t *testing.T, fault FaultType) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				switch fault {
				case FaultClientDisconnectRequest:
					// Читаем запрос (возможно, частичный), затем отвечаем 502
					br := bufio.NewReader(c)
					req := fasthttp.AcquireRequest()
					_ = req.Read(br) // читаем что успели
					fasthttp.ReleaseRequest(req)
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
					_ = bw.Flush()
				case FaultClientDisconnectResponseContentLength:
					// Отправляем заголовок с Content-Length: 1000, но только 500 байт тела.
					// Соединение закрывается — симуляция: upstream отправил все 1000 байт,
					// но fasthttp перестал читать после 500 (обрыв клиента).
					// PoolReader дочитывает до remain=0 и возвращает conn в пул
					// (соединение может быть мертвым — known issue для Content-Length).
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n")
					_, _ = bw.WriteString(strings.Repeat("x", 500))
					_ = bw.Flush()
				case FaultClientDisconnectResponseChunked:
					bw := bufio.NewWriter(c)
					_, _ = bw.WriteString("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n")
					_, _ = bw.WriteString("5\r\nhello\r\n")
					_ = bw.Flush()
				}
			}(conn)
		}
	}()
	return ln
}

// startPartialUpstream отправляет data и закрывает соединение.
func startPartialUpstream(t *testing.T, data string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				bw := bufio.NewWriter(c)
				_, _ = bw.WriteString(data)
				_ = bw.Flush()
			}(conn)
		}
	}()
	return ln
}
