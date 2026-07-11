package pool

import (
	"bufio"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"

	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
)

type MetricTrend int

const (
	_    MetricTrend = iota*2 - 3
	Down             // -1
	Up               // 1
)

// defaultBufSize — размер буфера по умолчанию для bufio.Writer и bufio.Reader.
// Может быть изменён через BufferSize. Значение по умолчанию — 4096.
// Минимальное допустимое значение — 64.
//
// Изменять значение рекомендуется до запуска сервера, поскольку пулы
// bufIOWriterPool и bufIOReaderPool создаются лениво при первом вызове
// AcquireBufIOWriter / AcquireBufIOReader. Уже существующие пулы
// используют старое значение до перезагрузки приложения.
//
// Пример:
//
//	pool.BufferSize(8192)
var defaultBufSize = 4096

// pipeCopyBufferSize — размер буфера для PipeCopy (64KB).
// Может быть изменён через PipeCopyBufferSize. Значение по умолчанию — 64 * 1024.
// Минимальное допустимое значение — 256.
//
// Изменять значение рекомендуется до запуска сервера, поскольку pipeCopyBufPool
// создаётся лениво при первом вызове PipeCopy. Уже существующий пул
// использует старое значение до перезагрузки приложения.
//
// ВАЖНО: PipeCopyBufferSize сбрасывает пул (старые буферы отбрасываются),
// так как изменился размер выделяемых объектов. Все текущие активные
// буферы остаются валидными, но после возврата в пул будут отброшены.
//
// Пример:
//
//	pool.PipeCopyBufferSize(128 * 1024) // 128KB
var pipeCopyBufferSize = 64 * 1024

// bufIOReaderPoolInUse — количество bufio.Reader, currently acquired (точное).
var bufIOReaderPoolInUse atomic.Int64

// bufIOReaderPoolIdle — количество bufio.Reader, idle в sync.Pool (приблизительное,
// GC может сбросить пул без учёта).
var bufIOReaderPoolIdle atomic.Int64

// bufIOReaderPool — пул bufio.Reader для readResponseHeaders (аналог fasthttp).
var bufIOReaderPool sync.Pool

// bufIOWriterPoolInUse — количество bufio.Writer, currently acquired (точное).
var bufIOWriterPoolInUse atomic.Int64

// bufIOWriterPoolIdle — количество bufio.Writer, idle в sync.Pool (приблизительное,
// GC может сбросить пул без учёта).
var bufIOWriterPoolIdle atomic.Int64

// bufIOWriterPool — пул bufio.Writer для writeRequestHeaders (аналог fasthttp).
var bufIOWriterPool sync.Pool

// pipeCopyBufPoolInUse — количество буферов, currently acquired (точное).
var pipeCopyBufPoolInUse atomic.Int64

// pipeCopyBufPoolIdle — количество буферов, idle в sync.Pool (приблизительное,
// GC может сбросить пул без учёта).
var pipeCopyBufPoolIdle atomic.Int64

// pipeCopyBufPool — пул буферов 64KB для PipeCopy (аналог copyBufPool в fasthttp).
var pipeCopyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, pipeCopyBufferSize)
		return b
	},
}

// BufferSize устанавливает размер буфера для bufio.Writer и bufio.Reader.
// По умолчанию — 4096. Минимальное допустимое значение — 64.
// При передаче значения меньше 64 функция логирует предупреждение и
// сохраняет текущее значение без изменений.
//
// Изменение применяется только для вновь создаваемых bufio.Writer/Reader;
// уже существующие объекты в пулах сохраняют старый размер.
func BufferSize(n int) {
	if n < 64 {
		log.Printf("pool: invalid BufferSize=%d, minimum is 64", n)
		return
	}
	defaultBufSize = n
}

// AcquireBufIOReader возвращает *bufio.Reader из пула или создаёт новый
// с буфером размера defaultBufSize. Reader сбрасывается на r.
//
// Метрики:
//   - bufIOReaderPoolInUse — увеличивается при каждом вызове
//   - bufIOReaderPoolIdle — уменьшается при успешном Get (v != nil)
//     Значения публикуются в Prometheus-gauges BufIOReaderPoolInUse и BufIOReaderPoolIdle.
func AcquireBufIOReader(r io.Reader) *bufio.Reader {
	v := bufIOReaderPool.Get()
	if v == nil {
		br := bufio.NewReaderSize(r, defaultBufSize)
		metricsBufIOReaderNew()

		return br
	}
	br := v.(*bufio.Reader)
	br.Reset(r)
	metricsBufIOReader(Up)

	return br
}

// ReleaseBufIOReader возвращает br обратно в пул для повторного использования.
func ReleaseBufIOReader(br *bufio.Reader) {
	bufIOReaderPool.Put(br)
	metricsBufIOReader(Down)
}

// AcquireBufIOWriter возвращает *bufio.Writer из пула или создаёт новый
// с буфером размера defaultBufSize. Writer сбрасывается на w.
//
// Метрики:
//   - bufIOWriterPoolInUse — увеличивается при каждом вызове
//   - bufIOWriterPoolIdle — уменьшается при успешном Get (v != nil)
//     Значения публикуются в Prometheus-gauges BufIOWriterPoolInUse и BufIOWriterPoolIdle.
func AcquireBufIOWriter(w io.Writer) *bufio.Writer {
	v := bufIOWriterPool.Get()
	if v == nil {
		bw := bufio.NewWriterSize(w, defaultBufSize)
		metricsBufIOWriterNew()

		return bw
	}
	bw := v.(*bufio.Writer)
	bw.Reset(w)
	metricsBufIOWriter(Up)

	return bw
}

// ReleaseBufIOWriter возвращает bw обратно в пул для повторного использования.
//
// Метрики:
//   - bufIOWriterPoolInUse — уменьшается
//   - bufIOWriterPoolIdle — увеличивается
func ReleaseBufIOWriter(bw *bufio.Writer) {
	bufIOWriterPool.Put(bw)
	metricsBufIOWriter(Down)
}

// PipeCopyBufferSize устанавливает размер буфера для PipeCopy.
// По умолчанию — 64 * 1024 (64KB). Минимальное допустимое значение — 256.
// При передаче значения меньше 256 функция логирует предупреждение и
// сохраняет текущее значение без изменений.
//
// ВАЖНО: при изменении размера пул pipeCopyBufPool сбрасывается (New-функция
// пересоздаётся), так как объекты разного размера несовместимы.
// Все текущие активные буферы остаются валидными, но после возврата
// в пул будут отброшены сборщиком мусора.
func PipeCopyBufferSize(n int) {
	if n < 256 {
		log.Printf("pool: invalid PipeCopyBufferSize=%d, minimum is 256", n)
		return
	}
	pipeCopyBufferSize = n
	pipeCopyBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, pipeCopyBufferSize)
			return b
		},
	}
	// Старые буферы в пуле более невалидны — сбрасываем idle
	pipeCopyBufPoolIdle.Store(0)
	metrics.PipeCopyBufPoolIdle.Set(0)
}

// PipeCopy копирует данные из src в dst, используя буфер 64KB из пула.
// Возвращает ошибку при неудачной записи.
//
// Метрики:
//   - pipeCopyBufPoolInUse — увеличивается при каждом Get, уменьшается при Put
//   - pipeCopyBufPoolIdle — уменьшается при успешном Get (v != nil), увеличивается при Put
//     Значения публикуются в Prometheus-gauges PipeCopyBufPoolInUse и PipeCopyBufPoolIdle.
func PipeCopy(src io.Reader, dst net.Conn) error {
	poolBuf := pipeCopyBufPool.Get()
	buf := poolBuf.([]byte)

	metricsUpPipeCopy()
	defer func() {
		pipeCopyBufPool.Put(poolBuf)
		metricsDownPipeCopy()
	}()

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

func metricsBufIOReaderNew() {
	bufIOReaderPoolInUse.Add(1)
	metrics.BufIOReaderPoolInUse.Set(float64(bufIOReaderPoolInUse.Load()))
	metrics.BufIOReaderPoolIdle.Set(float64(bufIOReaderPoolIdle.Load()))
}

// metricsBufIOReader Метрики:
//   - bufIOReaderPoolInUse — увеличивается
//   - bufIOReaderPoolIdle — уменьшается
func metricsBufIOReader(value MetricTrend) {
	bufIOReaderPoolInUse.Add(1 * int64(value))
	bufIOReaderPoolIdle.Add(-1 * int64(value))
	metrics.BufIOReaderPoolInUse.Set(float64(bufIOReaderPoolInUse.Load()))
	metrics.BufIOReaderPoolIdle.Set(float64(bufIOReaderPoolIdle.Load()))
}

func metricsBufIOWriterNew() {
	bufIOWriterPoolInUse.Add(1)
	metrics.BufIOWriterPoolInUse.Set(float64(bufIOWriterPoolInUse.Load()))
	metrics.BufIOWriterPoolIdle.Set(float64(bufIOWriterPoolIdle.Load()))
}

// metricsBufIOReader Метрики:
//   - bufIOReaderPoolInUse — увеличивается
//   - bufIOReaderPoolIdle — уменьшается
func metricsBufIOWriter(value MetricTrend) {
	bufIOReaderPoolInUse.Add(1 * int64(value))
	bufIOReaderPoolIdle.Add(-1 * int64(value))
	metrics.BufIOReaderPoolInUse.Set(float64(bufIOReaderPoolInUse.Load()))
	metrics.BufIOReaderPoolIdle.Set(float64(bufIOReaderPoolIdle.Load()))
}

func metricsDownPipeCopy() {
	pipeCopyBufPoolInUse.Add(-1)
	pipeCopyBufPoolIdle.Add(1)
	metrics.PipeCopyBufPoolInUse.Set(float64(pipeCopyBufPoolInUse.Load()))
	metrics.PipeCopyBufPoolIdle.Set(float64(pipeCopyBufPoolIdle.Load()))
}

func metricsUpPipeCopy() {
	pipeCopyBufPoolInUse.Add(1)

	// Если объект взят из пула (не новый), уменьшаем idle
	// Если poolBuf был создан New-функцией, idle не меняется.
	// Мы не можем отличить Get() нового от переиспользованного, поэтому
	// idle — приблизительное значение (GC может сбросить пул).
	pipeCopyBufPoolIdle.Add(-1)

	metrics.PipeCopyBufPoolInUse.Set(float64(pipeCopyBufPoolInUse.Load()))
	metrics.PipeCopyBufPoolIdle.Set(float64(pipeCopyBufPoolIdle.Load()))
}
