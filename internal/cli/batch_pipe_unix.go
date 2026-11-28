//go:build unix

package cli

import (
	"context"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func batchStateReader(ctx context.Context, input io.Reader) (io.Reader, error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	// Pipes and sockets can stay open without data. Agent hosts built on libuv,
	// such as Node.js, connect child stdin to a Unix socket rather than a pipe.
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return input, nil
	}
	conn, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}
	return &batchPipeReader{ctx: ctx, file: file, conn: conn}, nil
}

type batchPipeReader struct {
	ctx  context.Context
	file *os.File
	conn syscall.RawConn
}

// Closing an inherited blocking pipe or socket does not interrupt a Unix read. Wait
// for data or EOF before reading, without changing the pipe's blocking mode.
func (r *batchPipeReader) Read(b []byte) (int, error) {
	var pollErr error
	err := r.conn.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for r.ctx.Err() == nil {
			// The timeout bounds cancellation latency when the writer stays idle.
			n, err := unix.Poll(fds, 100)
			if err == unix.EINTR {
				continue
			}
			if err != nil || n > 0 {
				pollErr = err
				return
			}
		}
		pollErr = r.ctx.Err()
	})
	if err != nil {
		return 0, err
	}
	if pollErr != nil {
		return 0, pollErr
	}
	return r.file.Read(b)
}
