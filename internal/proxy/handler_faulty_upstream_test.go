package proxy

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// FaultType определяет тип сбоя для startFaultyUpstream.
type FaultType int

const (
	FaultNone                   FaultType = iota
	FaultCloseImmediate                   // закрыть соединение без отправки данных
	FaultPartialHeaders                   // отправить частичный заголовок и закрыть
	FaultContentLengthUnderread           // отправить полный заголовок, часть тела, закрыть
	FaultChunkedDisconnect                // отправить chunked-ответ без терминатора
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
