// Package proxy предоставляет HTTP-прокси со стримингом для fasthttp.
package proxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
	"github.com/vskurikhin/fasthttpproxy/internal/readers"
	"github.com/vskurikhin/fasthttpproxy/internal/upstream"
)

var upstreamsObj = upstream.NewUpstreams(nil)

// Handler возвращает fasthttp.RequestHandler, реализующий стриминговый прокси.
// upstreams — список upstream-серверов (host:port); если пуст, используется Host из запроса.
func Handler(upstreams []string) fasthttp.RequestHandler {
	upstreamsObj = upstream.NewUpstreams(upstreams)
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
| resolveUpstream        |  19    | Извлечение адреса из Host               |
| acquireUpstreamConn    |  12    | Получение соединения из пула            |
| writeRequestHeaders    |  17    | Запись + flush заголовков               |
| writeRequestBody       |  35    | Запись тела (stream/fixed)              |
| readResponseHeaders    |  12    | Чтение заголовков ответа                |
| copyResponseStatus     |  14    | Копирование статуса/заголовков          |
| streamResponseBody     |  11    | Установка стрим-тела                    |

Оркестратор handle() — 28 строк.
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
		pool.CloseAndDropUpstreamConnection(h.upstreamAddress, h.connection)
		return
	}
	if !h.writeRequestBody() {
		pool.CloseAndDropUpstreamConnection(h.upstreamAddress, h.connection)
		return
	}
	if !h.readResponseHeaders() {
		pool.CloseAndDropUpstreamConnection(h.upstreamAddress, h.connection)
		return
	}
	h.copyResponseStatus()
	h.streamResponseBody()
}

// resolveUpstream определяет адрес upstream.
// Если задан список upstream-серверов, выбирает случайный; иначе использует Host из запроса.
func (h *handler) resolveUpstream() bool {
	if up, ok := upstreamsObj.Random(); ok {
		h.upstreamAddress = up.Address
		return true
	}

	h.upstreamAddress = string(h.ctx.Host())
	if h.upstreamAddress == "" {
		log.Printf("no upstream address")
		h.ctx.Error("no host header", fasthttp.StatusBadRequest)
		return false
	}
	// Добавить схему по умолчанию, если не указана
	if !strings.HasPrefix(h.upstreamAddress, "http://") &&
		!strings.HasPrefix(h.upstreamAddress, "https://") {
		h.upstreamAddress = "http://" + h.upstreamAddress
	}
	_, err := url.Parse(h.upstreamAddress)
	if err != nil {
		h.ctx.Error("invalid upstream address", fasthttp.StatusBadRequest)
	}
	upstreamsObj.Append(h.upstreamAddress)
	return true
}

// acquireUpstreamConn получает соединение к upstream из пула.
// При ошибке возвращает 502 и завершает обработку.
func (h *handler) acquireUpstreamConn() bool {
	var err error
	h.connection, err = pool.AcquireUpstreamConnection(h.upstreamAddress)
	log.Printf("acquired connection: %v for upstream address: %s", h.connection, h.upstreamAddress)
	if err != nil {
		metrics.DialErrors.Inc()
		log.Printf("failed to acquire upstream connection: %s", err)
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
		log.Printf("failed to write request headers: %s", err)
		h.ctx.Error("upstream request header error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	if err := bw.Flush(); err != nil {
		metrics.BufIOWriterFlushErrors.Inc()
		log.Printf("upstream request header flush error: %s", err)
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
			log.Printf("failed to write request body: %s", err)
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
		log.Printf("failed to write request body: %s", err)
		h.ctx.Error("upstream request body write error: "+err.Error(), fasthttp.StatusBadGateway)
		return false
	}
	if n != len(body) {
		metrics.WriteErrors.Inc()
		log.Printf("failed to write request body: wrote %d of %d bytes", n, len(body))
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
		log.Printf("upstream response header read error: %s", err)
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
// Для фиксированного Content-Length ограничивает чтение через PoolReader.remain,
// для chunked/identity — прямой стрим до EOF (соединение закрывается).
func (h *handler) streamResponseBody() {
	h.ctx.Response.ImmediateHeaderFlush = true

	contentLen := h.responseHeader.ContentLength()
	tr := readers.NewTimedReader(h.bufIOReader, h.connection)
	// Передаём contentLen как remain:
	//   contentLen >= 0 — PoolReader вернёт соединение в пул после чтения.
	//   contentLen < 0  — PoolReader закроет соединение при EOF.
	pr := readers.NewPoolReader(tr, h.upstreamAddress, h.connection, int64(contentLen))
	h.ctx.SetBodyStream(pr, contentLen)
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
