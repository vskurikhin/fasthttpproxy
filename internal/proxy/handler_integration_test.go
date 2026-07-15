package proxy

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
)

// --- Интеграционные тесты: Upstream disconnect mid-response ---

// TestIntegrationUpstreamCloseImmediate проверяет, что при закрытии upstream
// без отправки данных через полный цикл Handler прокси возвращает 502.
//
// Сценарий: upstream принимает соединение и сразу закрывает.
// Handler → handle() → readResponseHeaders получает io.EOF → 502.
func TestIntegrationUpstreamCloseImmediate(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyUpstream(t, FaultCloseImmediate)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler([]string{ln.Addr().String()})
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
}

// TestIntegrationUpstreamPartialHeaders проверяет, что при частичном заголовке
// ответа upstream через полный цикл Handler прокси возвращает 502.
//
// Сценарий: upstream отправляет "HTTP/1.1 200 OK\r\n" и закрывает.
// readResponseHeaders получает неполный заголовок → ошибка → 502.
func TestIntegrationUpstreamPartialHeaders(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyUpstream(t, FaultPartialHeaders)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler([]string{ln.Addr().String()})
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
}

// TestIntegrationUpstreamContentLengthUnderread проверяет поведение прокси при
// неполном теле ответа upstream: заголовки с Content-Length: 100, но только
// 50 байт тела, затем закрытие.
//
// Сценарий B1: заголовки прочитаны успешно, тело неполное.
// handle() устанавливает body stream; PoolReader.readWithLimit получает EOF
// при remain=50 → ошибка. Статус остаётся 200 (заголовки уже скопированы).
func TestIntegrationUpstreamContentLengthUnderread(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyUpstream(t, FaultContentLengthUnderread)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler([]string{ln.Addr().String()})
	handler(&ctx)

	// handle() не должен упасть — body stream установлен корректно.
	// Статус 200 — заголовки скопированы до чтения тела.
	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	if !ctx.Response.ImmediateHeaderFlush {
		t.Fatal("expected ImmediateHeaderFlush")
	}

	// Читаем тело — должно быть меньше 100 байт
	body := ctx.Response.Body()
	if len(body) >= 100 {
		t.Fatalf("expected body < 100 bytes due to underread, got %d", len(body))
	}
	if len(body) == 0 {
		t.Fatal("expected some body bytes")
	}
}

// TestIntegrationUpstreamChunkedDisconnect проверяет, что при chunked-ответе
// без терминатора прокси корректно устанавливает body stream и не падает.
//
// Сценарий B2: upstream отправляет chunked-заголовок, один чанк, и закрывает
// без 0\r\n\r\n. PoolReader.readUntilEOF получает EOF → CloseAndDrop.
func TestIntegrationUpstreamChunkedDisconnect(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyUpstream(t, FaultChunkedDisconnect)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler([]string{ln.Addr().String()})
	handler(&ctx)

	// handle() не должен упасть — body stream установлен
	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	if !ctx.Response.ImmediateHeaderFlush {
		t.Fatal("expected ImmediateHeaderFlush")
	}

	// Пытаемся прочитать тело — fasthttp может отбросить неполный chunked-чанк
	body := ctx.Response.Body()
	_ = body // может быть пустым — допустимо для неполного chunked
}

// --- Интеграционные тесты: Client disconnect during streaming ---

// TestIntegrationClientDisconnectRequestBody проверяет, что при обрыве клиента
// во время передачи тела POST-запроса прокси возвращает 502 Bad Gateway.
//
// Сценарий: реальный upstream получает частичный запрос; клиентский body stream
// возвращает ошибку (симуляция обрыва). writeRequestBody() → false → 502.
func TestIntegrationClientDisconnectRequestBody(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyClientUpstream(t, FaultClientDisconnectRequest)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)
	// Body stream, который возвращает ошибку — симуляция обрыва клиента
	ctx.Request.SetBodyStream(&errReader{err: io.ErrUnexpectedEOF}, -1)

	h := &handler{ctx: &ctx}
	h.handle()

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
}

// TestIntegrationClientDisconnectResponseContentLength проверяет, что при обрыве
// клиента во время стриминга ответа с Content-Length прокси корректно
// завершает обработку: body stream установлен, статус 200, тело частичное.
//
// Сценарий: upstream отправляет 200 OK + Content-Length: 1000, но только 500 байт
// тела (симуляция: fasthttp перестал читать из-за обрыва клиента).
// PoolReader.readWithLimit дочитывает до remain=0 и вызывает ReleaseUpstreamConnection.
func TestIntegrationClientDisconnectResponseContentLength(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseContentLength)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	h := &handler{ctx: &ctx}
	h.handle()

	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", ctx.Response.StatusCode())
	}
	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	body := ctx.Response.Body()
	if len(body) == 0 {
		t.Fatal("expected non-empty body")
	}
	if len(body) > 500 {
		t.Fatalf("expected body <= 500 bytes (partial read), got %d", len(body))
	}
}

// TestIntegrationClientDisconnectResponseChunked проверяет, что при обрыве
// клиента во время стриминга chunked-ответа соединение upstream закрывается
// (не возвращается в пул). Второй запрос создаёт новое соединение.
//
// Сценарий: upstream отправляет chunked-заголовок, один чанк, и закрывает без
// 0\r\n\r\n. PoolReader.readUntilEOF получает EOF → CloseAndDropUpstreamConnection.
func TestIntegrationClientDisconnectResponseChunked(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseChunked)
	defer ln.Close()

	addr := ln.Addr().String()

	// Первый запрос — клиент оборвал, тело неполное
	var ctx1 fasthttp.RequestCtx
	var req1 fasthttp.Request
	req1.Header.SetMethod("GET")
	req1.SetRequestURI("/")
	req1.Header.SetHost(addr)
	ctx1.Init(&req1, nil, nil)

	h1 := &handler{ctx: &ctx1}
	h1.handle()

	if ctx1.Response.StatusCode() != 200 {
		t.Fatalf("first request: expected 200, got %d", ctx1.Response.StatusCode())
	}
	// Читаем тело — PoolReader.readUntilEOF получает EOF, соединение закрывается
	_ = ctx1.Response.Body()

	// Второй запрос — соединение было закрыто, создаётся новое
	var ctx2 fasthttp.RequestCtx
	var req2 fasthttp.Request
	req2.Header.SetMethod("GET")
	req2.SetRequestURI("/")
	req2.Header.SetHost(addr)
	ctx2.Init(&req2, nil, nil)

	h2 := &handler{ctx: &ctx2}
	h2.handle()

	if ctx2.Response.StatusCode() != 200 {
		t.Fatalf("second request: expected 200, got %d", ctx2.Response.StatusCode())
	}
}

// --- Интеграционные тесты: Dial timeout with pool full ---

// TestIntegrationPoolFull проверяет, что при переполненном пуле через полный
// цикл Handler прокси возвращает 502.
//
// Сценарий: заполняем пул напрямую, затем Handler получает ErrDialTimeout → 502.
func TestIntegrationPoolFull(t *testing.T) {
	restore := pool.SetMaxUpstreamConnectionsForTest(t, 2)
	defer restore()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			conn.Close()
		}
	}()
	addr := ln.Addr().String()
	poolAddr := "http://" + addr

	// Заполняем пул — 2 соединения для того же адреса, что использует handler
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		c, err := pool.AcquireUpstreamConnection(poolAddr)
		if err != nil {
			t.Fatalf("unexpected dial error at %d: %v", i, err)
		}
		conns = append(conns, c)
	}

	// Третий запрос через Handler — пул переполнен
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(addr)
	ctx.Init(&req, nil, nil)

	handler := Handler([]string{poolAddr})
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502 when pool full, got %d", ctx.Response.StatusCode())
	}

	for _, c := range conns {
		pool.ReleaseUpstreamConnection(poolAddr, c)
	}
}

// TestIntegrationDialTimeout проверяет, что при таймауте dial-функции через
// полный цикл Handler прокси возвращает 502.
//
// Сценарий: устанавливаем кастомную dial-функцию с таймаутом, вызываем Handler.
func TestIntegrationDialTimeout(t *testing.T) {
	restore := pool.SetCustomDialForTest(t, func(addr string) (net.Conn, error) {
		time.Sleep(50 * time.Millisecond)
		return nil, fasthttp.ErrDialTimeout
	})
	defer restore()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost("127.0.0.1:1")
	ctx.Init(&req, nil, nil)

	handler := Handler(nil)
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
}

// --- Тест copyResponseStatus с пустым respHeader ---

func TestCopyResponseStatusNilHeader(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:             &ctx,
		responseHeader:  nil,
		upstreamAddress: "example.com",
	}

	// Должен не запаниковать, а просто установить статус по умолчанию
	h.copyResponseStatus()
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("expected default status 200, got %d", ctx.Response.StatusCode())
	}
}
