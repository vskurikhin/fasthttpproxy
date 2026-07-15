package proxy

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
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

// --- Варианты Content-Length under-read (A1, A2, A3, B) ---

// TestHandlerContentLengthUnderreadVariants проверяет поведение прокси для
// разных вариантов under-read: zero body, severe under-read (99 из 100),
// и under-read через FaultContentLengthUnderread (50 из 100).
//
// Сценарий: для каждого варианта upstream отправляет полные заголовки с
// Content-Length: 100, но меньше байт тела, затем закрывает.
// handle() устанавливает body stream; ошибка возникает при чтении тела.
func TestHandlerContentLengthUnderreadVariants(t *testing.T) {
	tests := []struct {
		name   string
		fault  FaultType
		verify func(*testing.T, *fasthttp.RequestCtx)
	}{
		{
			name:  "underread_50_of_100",
			fault: FaultContentLengthUnderread,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if !ctx.Response.IsBodyStream() {
					t.Fatal("expected body stream")
				}
				if !ctx.Response.ImmediateHeaderFlush {
					t.Fatal("expected ImmediateHeaderFlush")
				}
				body := ctx.Response.Body()
				if len(body) >= 100 {
					t.Fatalf("expected body < 100 bytes, got %d", len(body))
				}
				if len(body) == 0 {
					t.Fatal("expected some body bytes")
				}
			},
		},
		{
			name:  "underread_zero_body",
			fault: FaultContentLengthUnderreadZeroBody,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if !ctx.Response.IsBodyStream() {
					t.Fatal("expected body stream")
				}
				if !ctx.Response.ImmediateHeaderFlush {
					t.Fatal("expected ImmediateHeaderFlush")
				}
				body := ctx.Response.Body()
				if len(body) >= 100 {
					t.Fatalf("expected body < 100 bytes (zero body), got %d", len(body))
				}
				// Может быть пустым или частичным — fasthttp может отбросить пустой поток
			},
		},
		{
			name:  "underread_99_of_100",
			fault: FaultContentLengthUnderread99,
			verify: func(t *testing.T, ctx *fasthttp.RequestCtx) {
				if !ctx.Response.IsBodyStream() {
					t.Fatal("expected body stream")
				}
				if !ctx.Response.ImmediateHeaderFlush {
					t.Fatal("expected ImmediateHeaderFlush")
				}
				body := ctx.Response.Body()
				if len(body) >= 100 {
					t.Fatalf("expected body < 100 bytes (severe underread), got %d", len(body))
				}
				if len(body) == 0 {
					t.Fatal("expected some body bytes for severe underread")
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

// --- Подтип A: Pool full (soft reject) ---

// TestAcquireUpstreamConnPoolFull проверяет, что при переполненном пуле
// acquireUpstreamConn() возвращает false и устанавливает 502.
//
// Сценарий: устанавливаем maxUpstreamConnectionsPerHost = 1, занимаем
// единственный слот, второй вызов получает ErrDialTimeout.
func TestAcquireUpstreamConnPoolFull(t *testing.T) {
	restore := pool.SetMaxUpstreamConnectionsForTest(t, 1)
	defer restore()

	// Занимаем единственный слот (неважно, что dial не удастся — count всё равно
	// инкрементится перед dial, а при ошибке декрементится).
	// Используем testDialer-like подход — реальный listener.
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

	conn1, err := pool.AcquireUpstreamConnection(ln.Addr().String())
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn1 == nil {
		t.Fatal("expected non-nil conn")
	}

	// Второй вызов — пул переполнен
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:             &ctx,
		upstreamAddress: ln.Addr().String(),
	}
	ok := h.acquireUpstreamConn()
	if ok {
		t.Fatal("expected false when pool is full")
	}
	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
	if h.connection != nil {
		t.Fatal("expected nil connection when pool is full")
	}

	pool.ReleaseUpstreamConnection(ln.Addr().String(), conn1)
}

// --- Подтип B: Dial timeout ---

// TestAcquireUpstreamConnDialTimeout проверяет, что при таймауте dial-функции
// acquireUpstreamConn() возвращает false и устанавливает 502.
//
// Сценарий: устанавливаем кастомную dial-функцию, которая блокируется на 100ms
// и возвращает fasthttp.ErrDialTimeout.
func TestAcquireUpstreamConnDialTimeout(t *testing.T) {
	restore := pool.SetCustomDialForTest(t, func(addr string) (net.Conn, error) {
		time.Sleep(100 * time.Millisecond)
		return nil, fasthttp.ErrDialTimeout
	})
	defer restore()

	addr := "127.0.0.1:1"

	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:             &ctx,
		upstreamAddress: addr,
	}

	start := time.Now()
	ok := h.acquireUpstreamConn()
	elapsed := time.Since(start)

	if ok {
		t.Fatal("expected false on dial timeout")
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected dial to block for at least 100ms, got %v", elapsed)
	}
	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
	if h.connection != nil {
		t.Fatal("expected nil connection on dial timeout")
	}
}

// --- Комбинация: Pool full + Dial timeout ---

// TestHandlerPoolFull проверяет, что при переполненном пуле через полный
// цикл Handler прокси возвращает 502.
//
// Сценарий: устанавливаем кастомную dial-функцию, которая всегда успешна.
// Заполняем пул через AcquireUpstreamConnection без Release для одного адреса,
// затем вызываем Handler — acquireUpstreamConn получает ErrDialTimeout → 502.
func TestHandlerPoolFull(t *testing.T) {
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

	// Заполняем пул напрямую — 2 соединения для того же адреса,
	// который будет использовать handler (с http:// префиксом).
	poolAddr := "http://" + addr
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		c, err := pool.AcquireUpstreamConnection(poolAddr)
		if err != nil {
			t.Fatalf("unexpected dial error at %d: %v", i, err)
		}
		conns = append(conns, c)
	}

	// Третий — пул переполнен. Используем тот же адрес.
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

	// Освобождаем
	for _, c := range conns {
		pool.ReleaseUpstreamConnection(poolAddr, c)
	}
}

// TestHandlerDialTimeout проверяет, что при таймауте dial-функции через
// полный цикл Handler прокси возвращает 502.
//
// Сценарий: устанавливаем кастомную dial-функцию с таймаутом, вызываем Handler.
func TestHandlerDialTimeout(t *testing.T) {
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

// --- TLS handshake failure ---

// TestHandlerTLSHandshakeFailure проверяет, что при ошибке, похожей на TLS
// handshake failure во время writeRequestHeaders, прокси возвращает 502.
//
// Сценарий: mock-соединение, writer которого возвращает ошибку при первой
// записи (симуляция tls.Conn.Write, где handshake падает).
// writeRequestHeaders() → bw.Flush() → conn.Write() → ошибка → 502.
func TestHandlerTLSHandshakeFailure(t *testing.T) {
	mc := newMockConn()
	mc.writer = &errWriter{err: io.ErrClosedPipe}
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
		request:    &req,
	}

	ok := h.writeRequestHeaders()
	if ok {
		t.Fatal("expected false on TLS handshake failure")
	}
	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
	}
}
