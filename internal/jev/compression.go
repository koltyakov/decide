package jev

import (
	"bytes"
	"compress/gzip"
	"io"
	"slices"

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

// compressBody keeps both the original and encoded body within the API limit.
func compressBody(body []byte, mode string) ([]byte, error) {
	if len(body) > MaxBytes {
		return nil, fault.New(fault.Usage, "uncompressed request exceeds 32 MiB")
	}
	if mode == "none" {
		return body, nil
	}
	var buffer bytes.Buffer
	output := &boundedBody{buffer: &buffer}
	var encoder io.WriteCloser
	if mode == "zstd" {
		writer, err := zstd.NewWriter(output, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(8<<20))
		if err != nil {
			return nil, err
		}
		encoder = writer
	} else {
		encoder = gzip.NewWriter(output)
	}
	_, writeErr := encoder.Write(body)
	closeErr := encoder.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
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
