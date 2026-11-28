//go:build !unix

package cli

import (
	"context"
	"io"
)

// On Windows os.File.Close interrupts pipe reads.
func batchStateReader(_ context.Context, input io.Reader) (io.Reader, error) {
	return input, nil
}
