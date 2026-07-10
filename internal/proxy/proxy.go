// Package proxy предоставляет HTTP-прокси со стримингом для fasthttp.
package proxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
)

// Handler возвращает fasthttp.RequestHandler, реализующий стриминговый прокси.
func Handler() fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		h := &handler{ctx: ctx}
		h.handle()
	}
}

type handler struct {
	ctx          *fasthttp.RequestCtx
	req          *fasthttp.Request
	upstreamAddr string
	conn         net.Conn
	br           *bufio.Reader
	respHeader   *fasthttp.ResponseHeader
}

/*
streamingProxyHandler (было 80 строк) декомпозирована на 7 методов по SRP:

| Метод                  | Строки | Ответственность                         |
|------------------------|:------:|-----------------------------------------|
| resolveUpstream        |   7    | Извлечение адреса из Host               |
| acquireUpstreamConn    |   8    | Получение соединения из пула            |
| writeRequestHeaders    |  14    | Запись + flush заголовков               |
| writeRequestBody       |  26    | Запись тела (stream/fixed)              |
| readResponseHeaders    |  11    | Чтение заголовков ответа                |
| copyResponseStatus     |   9    | Копирование статуса/заголовков          |
| streamResponseBody     |   9    | Установка стрим-тела                    |

Оркестратор handle() — 14 строк.
*/

func (h *handler) handle() {
	if !h.resolveUpstream() {
		return
	}

	h.req = fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(h.req)
	h.ctx.Request.CopyTo(h.req)

	if !h.acquireUpstreamConn() {
		return
	}
	defer pool.Put(h.upstreamAddr, h.conn)

	if !h.writeRequestHeaders() {
		return
	}
	if !h.writeRequestBody() {
		return
	}
	if !h.readResponseHeaders() {
		return
	}
	h.copyResponseStatus()
	h.streamResponseBody()
}

// resolveUpstream извлекает адрес upstream из Host заголовка запроса.
// При отсутствии Host возвращает 400 и завершает обработку.
func (h *handler) resolveUpstream() bool {
	h.upstreamAddr = string(h.ctx.Host())
	if h.upstreamAddr == "" {
		h.ctx.Error("no host header", fasthttp.StatusBadRequest)
		return false
	}
	return true
}

// acquireUpstreamConn получает соединение к upstream из пула.
// При ошибке возвращает 502 и завершает обработку.
func (h *handler) acquireUpstreamConn() bool {
	var err error
	h.conn, err = pool.Get(h.upstreamAddr)
	if err != nil {
		h.ctx.Error("cannot connect to upstream", fasthttp.StatusBadGateway)
		return false
	}
	return true
}

// writeRequestHeaders отправляет заголовки запроса upstream.
// При ошибке записи или сброса буфера возвращает 502 и завершает обработку.
func (h *handler) writeRequestHeaders() bool {
	bw := bufio.NewWriter(h.conn)

	if err := h.req.Header.Write(bw); err != nil {
		metrics.WriteErrors.Inc()
		h.ctx.Error("upstream request header error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	if err := bw.Flush(); err != nil {
		metrics.BufIOWriterFlushErrors.Inc()
		h.ctx.Error("upstream request flush error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	return true
}

// writeRequestBody передаёт тело запроса upstream.
// Для стримингового тела использует PipeCopy, для фиксированного — прямой Write.
// При ошибке записи или short write возвращает 502 и завершает обработку.
func (h *handler) writeRequestBody() bool {
	start := time.Now()
	defer func() {
		metrics.RequestBodyWriteDuration.Observe(time.Since(start).Seconds())
	}()

	if h.ctx.Request.IsBodyStream() {
		if err := PipeCopy(h.ctx.Request.BodyStream(), h.conn); err != nil {
			h.ctx.Error("upstream request body stream error: "+err.Error(), fasthttp.StatusBadGateway)
			return false
		}
		return true
	}

	body := h.ctx.Request.Body()
	if len(body) == 0 {
		return true
	}

	n, err := h.conn.Write(body)
	if err != nil {
		metrics.WriteErrors.Inc()
		h.ctx.Error("upstream request body write error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	if n != len(body) {
		metrics.WriteErrors.Inc()
		h.ctx.Error("upstream request body short write", fasthttp.StatusBadGateway)
		return false
	}
	return true
}

// readResponseHeaders читает заголовки ответа upstream.
// При ошибке чтения возвращает 502 и завершает обработку.
func (h *handler) readResponseHeaders() bool {
	h.br = bufio.NewReader(h.conn)

	h.respHeader = &fasthttp.ResponseHeader{}
	if err := h.respHeader.Read(h.br); err != nil {
		metrics.ReadErrors.Inc()
		h.ctx.Error("upstream response error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	return true
}

// copyResponseStatus копирует статус и заголовки ответа upstream клиенту.
// Если upstream вернул 5xx, заменяет статус на 502.
func (h *handler) copyResponseStatus() {
	if h.respHeader == nil {
		return
	}
	statusCode := h.respHeader.StatusCode()
	if statusCode >= 500 {
		metrics.Upstream5xx.Inc()
		h.ctx.Response.SetStatusCode(fasthttp.StatusBadGateway)
		log.Printf("upstream returned %d for %s", statusCode, h.upstreamAddr)
	} else {
		h.ctx.Response.SetStatusCode(statusCode)
		h.respHeader.CopyTo(&h.ctx.Response.Header)
	}
}

// streamResponseBody устанавливает стрим-тело ответа клиенту.
// Для фиксированного Content-Length использует LimitedReader,
// для chunked/identity — прямой стрим из буферизованного читателя.
func (h *handler) streamResponseBody() {
	h.ctx.Response.ImmediateHeaderFlush = true

	contentLen := h.respHeader.ContentLength()
	tr := &timedReader{r: h.br}
	if contentLen >= 0 {
		h.ctx.SetBodyStream(io.LimitReader(tr, int64(contentLen)), contentLen)
	} else {
		h.ctx.SetBodyStream(tr, -1)
	}
}

// timedReader оборачивает io.Reader и записывает время от первого Read до EOF в гистограмму.
type timedReader struct {
	r       io.Reader
	started bool
	start   time.Time
}

func (tr *timedReader) Read(p []byte) (int, error) {
	if !tr.started {
		tr.started = true
		tr.start = time.Now()
	}

	n, err := tr.r.Read(p)
	if err == io.EOF || (n > 0 && err != nil) {
		metrics.ResponseBodyReadDuration.Observe(time.Since(tr.start).Seconds())
	}
	return n, err
}

// PipeCopy копирует данные из src в dst, используя буфер 64KB.
// Возвращает ошибку при неудачной записи.
func PipeCopy(src io.Reader, dst net.Conn) error {
	buf := make([]byte, 64*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, errWrite := dst.Write(buf[:n]); errWrite != nil {
				return errWrite
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
