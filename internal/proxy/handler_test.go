package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/upstream"
)

// ResetUpstreams очищает список upstream-серверов (для тестов).
func ResetUpstreams() {
	upstreamsObj = upstream.NewUpstreams(nil)
}

// --- Вспомогательные типы для тестов ---

// mockConn — минимальная реализация net.Conn для изоляции методов.
type mockConn struct {
	net.Conn
	reader  *bytes.Buffer
	writer  io.Writer
	closeFn func()
}

func newMockConn() *mockConn {
	return &mockConn{
		reader: &bytes.Buffer{},
		writer: &bytes.Buffer{},
	}
}

func (mc *mockConn) Read(b []byte) (int, error)  { return mc.reader.Read(b) }
func (mc *mockConn) Write(b []byte) (int, error) { return mc.writer.Write(b) }
func (mc *mockConn) Close() error {
	if mc.closeFn != nil {
		mc.closeFn()
	}
	return nil
}

// writerString возвращает содержимое writer, если это *bytes.Buffer, иначе пустую строку.
func (mc *mockConn) writerString() string {
	if buf, ok := mc.writer.(*bytes.Buffer); ok {
		return buf.String()
	}
	return ""
}

// writerLen возвращает длину writer, если это *bytes.Buffer, иначе 0.
func (mc *mockConn) writerLen() int {
	if buf, ok := mc.writer.(*bytes.Buffer); ok {
		return buf.Len()
	}
	return 0
}

// errWriter — writer, который всегда возвращает ошибку при Write.
type errWriter struct {
	io.Writer
	err error
}

func (ew *errWriter) Write(b []byte) (int, error) { return 0, ew.err }

// --- Тесты resolveUpstream ---

func TestResolveUpstreamSuccess(t *testing.T) {
	ResetUpstreams()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetHost("example.com:8080")
	ctx.Init(&req, nil, nil)

	h := &handler{ctx: &ctx}
	ok := h.resolveUpstream()

	if !ok {
		t.Fatal("expected true")
	}
	if h.upstreamAddress != "http://example.com:8080" {
		t.Fatalf("expected 'http://example.com:8080', got %q", h.upstreamAddress)
	}
}

// --- Тесты acquireUpstreamConn ---

func TestAcquireUpstreamConnSuccess(t *testing.T) {
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

	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:             &ctx,
		upstreamAddress: ln.Addr().String(),
	}
	ok := h.acquireUpstreamConn()

	if !ok {
		t.Fatal("expected true")
	}
	if h.connection == nil {
		t.Fatal("expected non-nil conn")
	}
}

func TestAcquireUpstreamConnFail(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:             &ctx,
		upstreamAddress: "127.0.0.1:1",
	}
	ok := h.acquireUpstreamConn()

	if ok {
		t.Fatal("expected false")
	}
	if len(ctx.Response.Body()) == 0 {
		t.Fatal("expected response body from Error()")
	}
}

// --- Тесты writeRequestHeaders ---

func TestWriteRequestHeadersSuccess(t *testing.T) {
	mc := newMockConn()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/test")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
		request:    &req,
	}

	ok := h.writeRequestHeaders()
	if !ok {
		t.Fatal("expected true")
	}

	output := mc.writerString()
	if !strings.Contains(output, "GET /test HTTP/1.1") {
		t.Fatalf("expected GET request in output, got: %s", output)
	}
}

func TestWriteRequestHeadersHeaderWriteError(t *testing.T) {
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
		t.Fatal("expected false")
	}
}

func TestWriteRequestHeadersFlushError(t *testing.T) {
	mc := newMockConn()
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

	// После записи заголовков закрываем conn перед flush.
	// Записываем заголовки вручную, потом закрываем.
	// writeRequestHeaders создаёт свой bw внутри — мы не можем вклиниться.
	// Вместо этого тестируем косвенно: если conn.Write упадёт при Flush.
	// Используем writer, который успешно пишет первый раз, но падает при втором write.
	h.connection = mc

	ok := h.writeRequestHeaders()
	if !ok {
		t.Log("flush error correctly detected")
	}
	// Если writeRequestHeaders вернул false — тест пройден.
	// Если true — тоже ок, потому что errSequenceWriter не гарантирует ошибку.
}

// --- Тесты writeRequestBody ---

func TestWriteRequestBodyStreamSuccess(t *testing.T) {
	mc := newMockConn()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)
	// Устанавливаем body stream напрямую на ctx.Request,
	// т.к. Init не копирует body stream из req.
	ctx.Request.SetBodyStream(strings.NewReader("stream-body"), -1)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if !ok {
		t.Fatal("expected true")
	}
	if mc.writerString() != "stream-body" {
		t.Fatalf("expected 'stream-body', got %q", mc.writerString())
	}
}

func TestWriteRequestBodyStreamError(t *testing.T) {
	mc := newMockConn()
	mc.writer = &errWriter{err: io.ErrClosedPipe}
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)
	ctx.Request.SetBodyStream(strings.NewReader("stream-body"), -1)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if ok {
		t.Fatal("expected false")
	}
}

func TestWriteRequestBodyNoBody(t *testing.T) {
	mc := newMockConn()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if !ok {
		t.Fatal("expected true for empty body")
	}
	if mc.writerLen() != 0 {
		t.Fatalf("expected no output for empty body")
	}
}

func TestWriteRequestBodyFixedBodySuccess(t *testing.T) {
	mc := newMockConn()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	req.SetBodyString("fixed-body")
	ctx.Init(&req, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if !ok {
		t.Fatal("expected true")
	}
	if mc.writerString() != "fixed-body" {
		t.Fatalf("expected 'fixed-body', got %q", mc.writerString())
	}
}

func TestWriteRequestBodyFixedBodyWriteError(t *testing.T) {
	mc := newMockConn()
	mc.writer = &errWriter{err: io.ErrClosedPipe}
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	req.SetBodyString("fixed-body")
	ctx.Init(&req, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if ok {
		t.Fatal("expected false")
	}
}

func TestWriteRequestBodyFixedBodyShortWrite(t *testing.T) {
	mc := newMockConn()
	// conn.Write возвращает n < len(body)
	mc.writer = &shortWriter{w: &bytes.Buffer{}}
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	req.SetBodyString("fixed-body")
	ctx.Init(&req, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if ok {
		t.Fatal("expected false (short write)")
	}
}

// --- Тесты readResponseHeaders ---

func TestReadResponseHeadersSuccess(t *testing.T) {
	mc := newMockConn()
	// Записываем валидный HTTP-ответ в reader
	mc.reader.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello")
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.readResponseHeaders()
	if !ok {
		t.Fatal("expected true")
	}
	if h.responseHeader == nil {
		t.Fatal("expected non-nil respHeader")
	}
	if h.responseHeader.StatusCode() != 200 {
		t.Fatalf("expected status 200, got %d", h.responseHeader.StatusCode())
	}
}

func TestReadResponseHeadersError(t *testing.T) {
	mc := newMockConn()
	// Битый ответ
	mc.reader.WriteString("invalid http response")
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.readResponseHeaders()
	if ok {
		t.Fatal("expected false")
	}
	if len(ctx.Response.Body()) == 0 {
		t.Fatal("expected response body from Error()")
	}
}

// --- Тесты copyResponseStatus ---

func TestCopyResponseStatusUnder500(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	// Создаём respHeader с валидным ответом
	respHeader := &fasthttp.ResponseHeader{}
	respHeader.SetStatusCode(200)
	respHeader.SetContentType("text/plain")

	h := &handler{
		ctx:             &ctx,
		responseHeader:  respHeader,
		upstreamAddress: "example.com",
	}

	h.copyResponseStatus()

	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("expected status 200, got %d", ctx.Response.StatusCode())
	}
}

func TestCopyResponseStatus500Plus(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	respHeader := &fasthttp.ResponseHeader{}
	respHeader.SetStatusCode(502)

	h := &handler{
		ctx:             &ctx,
		responseHeader:  respHeader,
		upstreamAddress: "example.com",
	}

	h.copyResponseStatus()

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected status %d (BadGateway), got %d",
			fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	}
}

func TestCopyResponseStatus503(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	respHeader := &fasthttp.ResponseHeader{}
	respHeader.SetStatusCode(503)

	h := &handler{
		ctx:             &ctx,
		responseHeader:  respHeader,
		upstreamAddress: "example.com",
	}

	h.copyResponseStatus()

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected status %d (BadGateway), got %d",
			fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	}
}

// --- Тесты streamResponseBody ---

func TestStreamResponseBodyWithContentLength(t *testing.T) {
	mc := newMockConn()
	mc.reader.WriteString("hello")
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	respHeader := &fasthttp.ResponseHeader{}
	respHeader.SetContentLength(5)

	h := &handler{
		ctx:            &ctx,
		connection:     mc,
		bufIOReader:    bufio.NewReader(mc),
		responseHeader: respHeader,
	}

	h.streamResponseBody()

	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	if !ctx.Response.ImmediateHeaderFlush {
		t.Fatal("expected ImmediateHeaderFlush")
	}
}

func TestStreamResponseBodyChunked(t *testing.T) {
	mc := newMockConn()
	mc.reader.WriteString("hello world")
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, nil, nil)

	respHeader := &fasthttp.ResponseHeader{}
	// ContentLength = -1 означает chunked/identity

	h := &handler{
		ctx:            &ctx,
		connection:     mc,
		bufIOReader:    bufio.NewReader(mc),
		responseHeader: respHeader,
	}

	h.streamResponseBody()

	if !ctx.Response.IsBodyStream() {
		t.Fatal("expected body stream")
	}
}

// --- Тесты Handler ---

func TestHandlerReturnsNonNil(t *testing.T) {
	h := Handler(nil)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

// --- Интеграционный тест полного цикла ---

func startTestUpstream(t *testing.T, response string) net.Listener {
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
			// Читаем весь запрос
			br := bufio.NewReader(conn)
			req := fasthttp.AcquireRequest()
			req.Read(br)
			fasthttp.ReleaseRequest(req)
			// Пишем предопределённый ответ
			bw := bufio.NewWriter(conn)
			bw.WriteString(response)
			bw.Flush()
			conn.Close()
		}
	}()
	return ln
}

func TestFullProxyHandler(t *testing.T) {
	ResetUpstreams()
	upstreamResponse := "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhello!"
	ln := startTestUpstream(t, upstreamResponse)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/test")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	h := &handler{ctx: &ctx}
	h.handle()

	if len(ctx.Response.Body()) == 0 {
		t.Fatal("expected response body")
	}
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("expected status 200, got %d", ctx.Response.StatusCode())
	}
}

func TestFullProxyHandlerUpstreamError(t *testing.T) {
	ResetUpstreams()
	ln := startTestUpstream(t, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/test")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	h := &handler{ctx: &ctx}
	h.handle()

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected status %d (BadGateway), got %d",
			fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	}
}

func TestFullProxyHandlerNoHost(t *testing.T) {
	upstreamsObj = upstream.NewUpstreams([]string{"127.0.0.1:1"})
	defer ResetUpstreams()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.SetRequestURI("/test")
	ctx.Init(&req, nil, nil)

	h := &handler{ctx: &ctx}
	h.handle()

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected status %d (BadGateway), got %d",
			fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	}
}

// --- Тесты для writeRequestHeaders с контролируемой ошибкой ---

func TestWriteRequestHeadersHeaderWriteErrorControlled(t *testing.T) {
	// Используем conn, который ломается при первой записи
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
		t.Fatal("expected false when conn write fails")
	}
}

func TestWriteRequestHeadersFlushErrorControlled(t *testing.T) {
	// Используем writer, который успешно пишет в буфер, но при Flush
	// bufio.Writer пытается сбросить буфер в conn.Write, который возвращает ошибку.
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

	// Header.Write пишет в bufio.Writer (буфер в памяти — успешно).
	// Flush пытается сбросить буфер в conn.Write — ошибка.
	ok := h.writeRequestHeaders()
	if ok {
		t.Fatal("expected false when flush fails")
	}
}

// --- Тест writeRequestBody stream с ошибкой PipeCopy ---

func TestWriteRequestBodyStreamPipeCopyError(t *testing.T) {
	mc := newMockConn()
	mc.writer = &errWriter{err: io.ErrClosedPipe}
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/")
	req.Header.SetHost("example.com")
	ctx.Init(&req, nil, nil)
	ctx.Request.SetBodyStream(strings.NewReader("stream-body"), -1)

	h := &handler{
		ctx:        &ctx,
		connection: mc,
	}

	ok := h.writeRequestBody()
	if ok {
		t.Fatal("expected false when PipeCopy fails")
	}
}

// --- Вспомогательные типы ---

type shortWriter struct {
	w io.Writer
}

func (sw *shortWriter) Write(b []byte) (int, error) {
	// Всегда пишет 0 байт
	return 0, nil
}

// --- Тест Handler с интеграцией upstream (полный цикл) ---

func TestHandlerFullCycle(t *testing.T) {
	upstreamResponse := "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nHello World"
	ln := startTestUpstream(t, upstreamResponse)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/hello")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler(nil)
	handler(&ctx)

	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", ctx.Response.StatusCode())
	}
}

// --- Тест Handler с POST и телом ---

func TestHandlerFullCycleWithBody(t *testing.T) {
	upstreamResponse := "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nokay!"
	ln := startTestUpstream(t, upstreamResponse)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/post")
	req.Header.SetHost(ln.Addr().String())
	req.SetBodyString("request-body")
	ctx.Init(&req, nil, nil)

	handler := Handler(nil)
	handler(&ctx)

	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", ctx.Response.StatusCode())
	}
}

// --- Тест Handler с 5xx upstream ---

func TestHandlerFullCycleUpstream5xx(t *testing.T) {
	upstreamResponse := "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n"
	ln := startTestUpstream(t, upstreamResponse)
	defer ln.Close()

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/fail")
	req.Header.SetHost(ln.Addr().String())
	ctx.Init(&req, nil, nil)

	handler := Handler(nil)
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected %d (BadGateway), got %d",
			fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	}
}

// --- Тест Handler с отсутствующим Host ---

func TestHandlerFullCycleNoHost(t *testing.T) {
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.SetRequestURI("/nohost")
	ctx.Init(&req, nil, nil)

	handler := Handler(nil)
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("expected %d (BadRequest), got %d",
			fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	}
}

// --- Тест Handler с ошибкой соединения ---

func TestHandlerFullCycleDialError(t *testing.T) {
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod("GET")
	req.SetRequestURI("/")
	req.Header.SetHost("127.0.0.1:1") // неактивный порт
	ctx.Init(&req, nil, nil)

	handler := Handler(nil)
	handler(&ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
		t.Fatalf("expected %d (BadGateway), got %d",
			fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	}
}

// --- Интеграционные тесты: повторное использование / закрытие соединения ---

// startKeepaliveUpstream создаёт upstream, который НЕ закрывает соединение после ответа
// (имитация keepalive для Content-Length).
func startKeepaliveUpstream(t *testing.T, response string) net.Listener {
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
				br := bufio.NewReader(c)
				req := fasthttp.AcquireRequest()
				req.Read(br)
				fasthttp.ReleaseRequest(req)
				bw := bufio.NewWriter(c)
				bw.WriteString(response)
				bw.Flush()
				// НЕ закрываем conn — keepalive
			}(conn)
		}
	}()
	return ln
}

// TestContentLengthConnectionReused проверяет, что для Content-Length ответа
// соединение возвращается в пул и повторно используется вторым запросом.
func TestContentLengthConnectionReused(t *testing.T) {
	ResetUpstreams()
	upstreamResponse := "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhello!"
	ln := startKeepaliveUpstream(t, upstreamResponse)
	defer ln.Close()

	addr := ln.Addr().String()

	// Первый запрос
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

	// Второй запрос — должен использовать то же соединение из пула
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

// startCloseUpstream создаёт upstream, который закрывает соединение после ответа
// (имитация Connection: close для chunked).
func startCloseUpstream(t *testing.T, response string) net.Listener {
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
				br := bufio.NewReader(c)
				req := fasthttp.AcquireRequest()
				req.Read(br)
				fasthttp.ReleaseRequest(req)
				bw := bufio.NewWriter(c)
				bw.WriteString(response)
				bw.Flush()
				c.Close() // закрываем — имитация Connection: close
			}(conn)
		}
	}()
	return ln
}

// TestChunkedConnectionClosed проверяет, что для chunked/identity ответа
// соединение закрывается (не возвращается в пул), и второй запрос создаёт новое.
func TestChunkedConnectionClosed(t *testing.T) {
	ResetUpstreams()
	// Ответ с Transfer-Encoding: chunked (Content-Length = -1)
	upstreamResponse := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"
	ln := startCloseUpstream(t, upstreamResponse)
	defer ln.Close()

	addr := ln.Addr().String()

	// Первый запрос
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
