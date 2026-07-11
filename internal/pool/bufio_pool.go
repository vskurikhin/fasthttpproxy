package pool

import (
	"bufio"
	"io"
	"log"
	"net"
	"sync"
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
//	pool.SetDefaultBufSize(8192)
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

// bufIOWriterPool — пул bufio.Writer для writeRequestHeaders (аналог fasthttp).
var bufIOWriterPool sync.Pool

// bufIOReaderPool — пул bufio.Reader для readResponseHeaders (аналог fasthttp).
var bufIOReaderPool sync.Pool

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
}

// AcquireBufIOWriter возвращает *bufio.Writer из пула или создаёт новый
// с буфером размера defaultBufSize. Writer сбрасывается на w.
func AcquireBufIOWriter(w io.Writer) *bufio.Writer {
	v := bufIOWriterPool.Get()
	if v == nil {
		return bufio.NewWriterSize(w, defaultBufSize)
	}
	bw := v.(*bufio.Writer)
	bw.Reset(w)
	return bw
}

// ReleaseBufIOWriter возвращает bw обратно в пул для повторного использования.
func ReleaseBufIOWriter(bw *bufio.Writer) {
	bufIOWriterPool.Put(bw)
}

// AcquireBufIOReader возвращает *bufio.Reader из пула или создаёт новый
// с буфером размера defaultBufSize. Reader сбрасывается на r.
func AcquireBufIOReader(r io.Reader) *bufio.Reader {
	v := bufIOReaderPool.Get()
	if v == nil {
		return bufio.NewReaderSize(r, defaultBufSize)
	}
	br := v.(*bufio.Reader)
	br.Reset(r)
	return br
}

// ReleaseBufIOReader возвращает br обратно в пул для повторного использования.
func ReleaseBufIOReader(br *bufio.Reader) {
	bufIOReaderPool.Put(br)
}

// PipeCopy копирует данные из src в dst, используя буфер 64KB из пула.
// Возвращает ошибку при неудачной записи.
func PipeCopy(src io.Reader, dst net.Conn) error {
	poolBuf := pipeCopyBufPool.Get()
	buf := poolBuf.([]byte)
	defer pipeCopyBufPool.Put(poolBuf)

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
