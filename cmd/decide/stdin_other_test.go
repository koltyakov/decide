//go:build !unix

package main

// socketStdin is nil where Unix socket stdin is not available.
var socketStdin stdinSource
