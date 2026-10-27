package jev

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/koltyakov/decide/internal/fault"
)

func TestCompressionRoundTripAndWindow(t *testing.T) {
	body := bytes.Repeat([]byte("private input "), 1<<20)
	for _, mode := range []string{"none", "gzip", "x-gzip", "zstd"} {
		t.Run(mode, func(t *testing.T) {
			encoded, err := compressBody(body, mode)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []byte
			switch mode {
			case "none":
				decoded = encoded
			case "zstd":
				var header zstd.Header
				if err := header.Decode(encoded); err != nil {
					t.Fatal(err)
				}
				if header.WindowSize > 8<<20 || (header.SingleSegment && header.FrameContentSize > 8<<20) {
					t.Fatal("zstd window exceeds API limit", header)
				}
				reader, err := zstd.NewReader(bytes.NewReader(encoded), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(8<<20))
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				decoded, err = io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
			default:
				reader, err := gzip.NewReader(bytes.NewReader(encoded))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err = io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(decoded, body) {
				t.Fatal("compression changed the body")
			}
		})
	}
}

func TestCompressionLimits(t *testing.T) {
	body := make([]byte, MaxBytes+1)
	for _, mode := range []string{"none", "gzip", "x-gzip", "zstd"} {
		if _, err := compressBody(body, mode); fault.Code(err) != fault.Usage {
			t.Fatalf("%s accepted oversized uncompressed body: %v", mode, err)
		}
	}
	buffer := bytes.NewBuffer(body[:MaxBytes-1])
	writer := &boundedBody{buffer: buffer}
	if _, err := writer.Write([]byte{1, 2}); fault.Code(err) != fault.Usage || buffer.Len() != MaxBytes-1 {
		t.Fatal("compressed output exceeded its bounded buffer", err)
	}
	if n, err := writer.Write([]byte{1}); n != 1 || err != nil || buffer.Len() != MaxBytes {
		t.Fatal("inclusive body limit rejected", err)
	}
}
