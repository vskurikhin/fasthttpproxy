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
	"github.com/vskurikhin/fasthttpproxy/internal/readers"
)

// Handler возвращает fasthttp.RequestHandler, реализующий стриминговый прокси.
func Handler() fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		h := &handler{ctx: ctx}
		h.handle()
	}
}

type handler struct {
	ctx             *fasthttp.RequestCtx
	request         *fasthttp.Request
	upstreamAddress string
	connection      net.Conn
	bufIOReader     *bufio.Reader
	responseHeader  *fasthttp.ResponseHeader
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

	h.request = fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(h.request)
	h.ctx.Request.CopyTo(h.request)

	if !h.acquireUpstreamConn() {
		return
	}

	if !h.writeRequestHeaders() {
		pool.Put(h.upstreamAddress, h.connection)
		return
	}
	if !h.writeRequestBody() {
		pool.Put(h.upstreamAddress, h.connection)
		return
	}
	if !h.readResponseHeaders() {
		pool.Put(h.upstreamAddress, h.connection)
		return
	}
	h.copyResponseStatus()
	h.streamResponseBody()
}

// resolveUpstream извлекает адрес upstream из Host заголовка запроса.
// При отсутствии Host возвращает 400 и завершает обработку.
func (h *handler) resolveUpstream() bool {
	h.upstreamAddress = string(h.ctx.Host())
	if h.upstreamAddress == "" {
		h.ctx.Error("no host header", fasthttp.StatusBadRequest)
		return false
	}
	return true
}

// acquireUpstreamConn получает соединение к upstream из пула.
// При ошибке возвращает 502 и завершает обработку.
func (h *handler) acquireUpstreamConn() bool {
	var err error
	h.connection, err = pool.Get(h.upstreamAddress)
	if err != nil {
		h.ctx.Error("cannot connect to upstream", fasthttp.StatusBadGateway)
		return false
	}
	return true
}

// writeRequestHeaders отправляет заголовки запроса upstream.
// При ошибке записи или сброса буфера возвращает 502 и завершает обработку.
func (h *handler) writeRequestHeaders() bool {
	bw := bufio.NewWriter(h.connection)

	if err := h.request.Header.Write(bw); err != nil {
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
		if err := PipeCopy(h.ctx.Request.BodyStream(), h.connection); err != nil {
			h.ctx.Error("upstream request body stream error: "+err.Error(), fasthttp.StatusBadGateway)
			return false
		}
		return true
	}

	body := h.ctx.Request.Body()
	if len(body) == 0 {
		return true
	}

	n, err := h.connection.Write(body)
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
	h.bufIOReader = bufio.NewReader(h.connection)

	h.responseHeader = &fasthttp.ResponseHeader{}
	if err := h.responseHeader.Read(h.bufIOReader); err != nil {
		metrics.ReadErrors.Inc()
		h.ctx.Error("upstream response error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	return true
}

// copyResponseStatus копирует статус и заголовки ответа upstream клиенту.
// Если upstream вернул 5xx, заменяет статус на 502.
func (h *handler) copyResponseStatus() {
	if h.responseHeader == nil {
		return
	}
	statusCode := h.responseHeader.StatusCode()
	if statusCode >= 500 {
		metrics.Upstream5xx.Inc()
		h.ctx.Response.SetStatusCode(fasthttp.StatusBadGateway)
		log.Printf("upstream returned %d for %s", statusCode, h.upstreamAddress)
	} else {
		h.ctx.Response.SetStatusCode(statusCode)
		h.responseHeader.CopyTo(&h.ctx.Response.Header)
	}
}

// streamResponseBody устанавливает стрим-тело ответа клиенту.
// Для фиксированного Content-Length использует LimitedReader,
// для chunked/identity — прямой стрим из буферизованного читателя.
func (h *handler) streamResponseBody() {
	h.ctx.Response.ImmediateHeaderFlush = true

	contentLen := h.responseHeader.ContentLength()
	tr := readers.NewTimedReader(h.bufIOReader)
	pr := readers.NewPoolReader(tr, h.upstreamAddress, h.connection)
	if contentLen >= 0 {
		h.ctx.SetBodyStream(io.LimitReader(pr, int64(contentLen)), contentLen)
	} else {
		h.ctx.SetBodyStream(pr, -1)
	}
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
