package main

import "os"

// cmdRpc is `node`: older callers start `rpc --stdio`.
func cmdRpc(args []string) error { return cmdNode(args) }

type stdPipes struct {
	*os.File
	w *os.File
}

func (s stdPipes) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s stdPipes) Close() error                { s.w.Close(); return s.File.Close() }
