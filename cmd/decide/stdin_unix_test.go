//go:build unix

package main

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// socketStdin gives the child a Unix socket as stdin, as libuv-based hosts
// such as Node.js do for piped child processes.
var socketStdin stdinSource = func(t *testing.T, cmd *exec.Cmd) io.WriteCloser {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	parent, child := os.NewFile(uintptr(fds[0]), "stdin-parent"), os.NewFile(uintptr(fds[1]), "stdin-child")
	t.Cleanup(func() { _ = child.Close() })
	cmd.Stdin = child
	return parent
}
