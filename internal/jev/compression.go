package jev

import (
	"bytes"
	"io"
	"slices"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/koltyakov/decide/internal/fault"
)

func validateCompression(mode string, capabilities Capabilities) error {
	if mode == "none" {
		return nil
	}
	if mode != "gzip" && mode != "x-gzip" && mode != "zstd" {
		return fault.New(fault.Usage, "compression must be none, gzip, x-gzip, or zstd")
	}
	if !slices.Contains(capabilities.Compression, mode) {
		return fault.New(fault.Usage, "request compression is supported only on official/custom routes")
	}
	return nil
}

// Encoders are expensive to allocate, so batches reuse them. Each pooled
// encoder is used by one request at a time and reset before use.
var (
	zstdEncoders sync.Pool
	gzipEncoders sync.Pool
)

type encoder interface {
	io.WriteCloser
	Reset(io.Writer)
}

func newEncoder(mode string, w io.Writer) (encoder, *sync.Pool, error) {
	pool := &gzipEncoders
	if mode == "zstd" {
		pool = &zstdEncoders
	}
	if e, ok := pool.Get().(encoder); ok {
		e.Reset(w)
		return e, pool, nil
	}
	if mode == "zstd" {
		e, err := zstd.NewWriter(w, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(8<<20))
		return e, pool, err
	}
	return gzip.NewWriter(w), pool, nil
}

// compressBody keeps both the original and encoded body within the API limit.
func compressBody(body []byte, mode string) ([]byte, error) {
	if len(body) > MaxBytes {
		return nil, fault.New(fault.Usage, "uncompressed request exceeds 32 MiB")
	}
	if mode == "none" {
		return body, nil
	}
	var buffer bytes.Buffer
	buffer.Grow(min(len(body)/2+512, MaxBytes))
	output := &boundedBody{buffer: &buffer}
	e, pool, err := newEncoder(mode, output)
	if err != nil {
		return nil, err
	}
	_, writeErr := e.Write(body)
	closeErr := e.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// Only a cleanly closed encoder is reused; a failed one is dropped.
	e.Reset(nil)
	pool.Put(e)
	return buffer.Bytes(), nil
}

type boundedBody struct {
	buffer *bytes.Buffer
}

func (w *boundedBody) Write(data []byte) (int, error) {
	if len(data) > MaxBytes-w.buffer.Len() {
		return 0, fault.New(fault.Usage, "compressed request exceeds 32 MiB")
	}
	return w.buffer.Write(data)
}
