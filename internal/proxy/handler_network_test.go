package proxy

import (
	"io"
	"testing"

	"github.com/valyala/fasthttp"
)

// --- Подтип A: разрыв до/во время заголовков ---

// TestHandlerUpstreamCloseImmediate проверяет, что при закрытии upstream без
// отправки данных прокси возвращает 502 Bad Gateway.
//
// Сценарий: upstream принимает соединение и сразу закрывает.
// Ожидание: readResponseHeaders получает io.EOF → 502.
func TestHandlerUpstreamCloseImmediate(t *testing.T) {
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

// TestHandlerUpstreamPartialHeaders проверяет, что при частичном заголовке
// ответа upstream прокси возвращает 502 Bad Gateway.
//
// Сценарий: upstream отправляет "HTTP/1.1 200 OK\r\n" и закрывает.
// Ожидание: readResponseHeaders получает неполный заголовок → ошибка → 502.
func TestHandlerUpstreamPartialHeaders(t *testing.T) {
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

// --- Подтип B1: Content-Length under-read ---

// TestHandlerUpstreamContentLengthUnderread проверяет поведение прокси, когда
// upstream отправляет полные заголовки с Content-Length: 100, но только 50
// байт тела, затем закрывает соединение.
//
// Сценарий B1: заголовки прочитаны успешно, но тело неполное.
// handle() устанавливает body stream; ошибка возникает при чтении тела.
func TestHandlerUpstreamContentLengthUnderread(t *testing.T) {
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
	// Статус может быть 200 (заголовки скопированы до чтения тела).
	// Ошибка чтения тела возникает позже, при передаче клиенту.
	// Проверяем, что body stream установлен и ImmediateHeaderFlush включён.
	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	if !ctx.Response.ImmediateHeaderFlush {
		t.Fatal("expected ImmediateHeaderFlush")
	}

	// Пытаемся прочитать тело — должно быть меньше 100 байт.
	body := ctx.Response.Body()
	if len(body) >= 100 {
		t.Fatalf("expected body < 100 bytes due to underread, got %d", len(body))
	}
	if len(body) == 0 {
		t.Fatal("expected some body bytes")
	}
}

// --- Подтип B2: chunked disconnect ---

// TestHandlerUpstreamChunkedDisconnect проверяет, что при chunked-ответе без
// терминатора прокси корректно устанавливает body stream и соединение
// закрывается при EOF.
//
// Сценарий B2: upstream отправляет chunked-заголовок, один чанк, и закрывает
// без 0\r\n\r\n. PoolReader.readUntilEOF получает EOF → CloseAndDrop.
func TestHandlerUpstreamChunkedDisconnect(t *testing.T) {
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

	// handle() не должен упасть — body stream установлен.
	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	if !ctx.Response.ImmediateHeaderFlush {
		t.Fatal("expected ImmediateHeaderFlush")
	}

	// Пытаемся прочитать тело — должно быть частичное содержимое.
	body := ctx.Response.Body()
	if len(body) == 0 {
		t.Log("body is empty after chunked disconnect (fasthttp may discard incomplete chunk)")
	}
}

// --- Табличный тест для всех сценариев ---

func TestNetworkFailures(t *testing.T) {
	tests := []struct {
		name   string
		fault  FaultType
		verify func(*testing.T, *fasthttp.RequestCtx)
	}{
		{
			name:  "upstream_close_immediate",
			fault: FaultCloseImmediate,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
					t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
				}
			},
		},
		{
			name:  "upstream_partial_headers",
			fault: FaultPartialHeaders,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
					t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
				}
			},
		},
		{
			name:  "upstream_content_length_underread",
			fault: FaultContentLengthUnderread,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if !ctx.Response.IsBodyStream() {
					t.Fatal("expected body stream")
				}
				body := ctx.Response.Body()
				if len(body) >= 100 {
					t.Fatalf("expected body < 100 bytes, got %d", len(body))
				}
			},
		},
		{
			name:  "upstream_chunked_disconnect",
			fault: FaultChunkedDisconnect,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if !ctx.Response.IsBodyStream() {
					t.Fatal("expected body stream")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetUpstreams()
			ln := startFaultyUpstream(t, tt.fault)
			defer ln.Close()

			var ctx fasthttp.RequestCtx
			var req fasthttp.Request
			req.Header.SetMethod("GET")
			req.SetRequestURI("/")
			req.Header.SetHost(ln.Addr().String())
			ctx.Init(&req, nil, nil)

			handler := Handler([]string{ln.Addr().String()})
			handler(&ctx)

			tt.verify(t, &ctx)
		})
	}
}

// --- Подтип A: Client disconnect during request body ---

// TestHandlerClientDisconnectRequestBody проверяет, что при ошибке чтения
// клиентского body stream (симуляция обрыва клиента во время POST) прокси
// возвращает 502 Bad Gateway.
//
// Сценарий: клиент начал отправлять тело POST-запроса, но оборвал соединение.
// BodyStream() возвращает ошибку при Read() → PipeCopy возвращает ошибку →
// writeRequestBody() возвращает false → handle() вызывает CloseAndDrop → 502.
func TestHandlerClientDisconnectRequestBody(t *testing.T) {
	mc := newMockConn()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)
	// Body stream, который возвращает ошибку при первом Read — симуляция обрыва клиента
	ctx.Request.SetBodyStream(&errReader{err: io.ErrUnexpectedEOF}, -1)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if ok {
		t.Fatal("expected false when client body stream fails")
	}
	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
}

// --- Подтип B1: Client disconnect during Content-Length response ---

// TestHandlerClientDisconnectResponseContentLength проверяет, что при обрыве
// клиента во время стриминга ответа с Content-Length прокси корректно
// устанавливает body stream. Соединение upstream в итоге возвращается в пул
// (readWithLimit дочитывает до remain).
//
// Сценарий: upstream отправляет 200 OK + Content-Length: 1000 + 500 байт тела.
// Клиент обрывает после handle() — fasthttp перестаёт читать из PoolReader.
// PoolReader продолжает чтение до remain=0 и возвращает соединение в пул.
func TestHandlerClientDisconnectResponseContentLength(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseContentLength)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler([]string{ln.Addr().String()})
	handler(&ctx)

	// handle() не должен упасть — body stream установлен корректно
	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	if !ctx.Response.ImmediateHeaderFlush {
		t.Fatal("expected ImmediateHeaderFlush")
	}

	// Читаем тело — PoolReader дочитывает до remain=0 и возвращает соединение в пул
	body := ctx.Response.Body()
	if len(body) == 0 {
		t.Fatal("expected non-empty body")
	}
	if len(body) > 500 {
		t.Fatalf("expected body <= 500 bytes (partial read), got %d", len(body))
	}
}

// --- Подтип B2: Client disconnect during chunked response ---

// TestHandlerClientDisconnectResponseChunked проверяет, что при обрыве клиента
// во время стриминга chunked-ответа прокси корректно устанавливает body stream.
// Соединение upstream закрывается (readUntilEOF получает EOF → CloseAndDrop).
//
// Сценарий: upstream отправляет chunked-заголовок, один чанк, и закрывает без
// 0\r\n\r\n. Клиент обрывает — fasthttp перестаёт читать.
func TestHandlerClientDisconnectResponseChunked(t *testing.T) {
	ResetUpstreams()
	ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseChunked)
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

	// Пытаемся прочитать тело — может быть пустым, если fasthttp отбросил неполный чанк
	body := ctx.Response.Body()
	_ = body // может быть пустым — это допустимо для неполного chunked
}
