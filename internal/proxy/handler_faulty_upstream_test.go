package proxy

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

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
