package main

import "syscall"

func checkerror(err error) {
	if err != nil {
		panic(err)
	}
}

func checkok(ok bool) {
	if !ok {
		panic("")
	}
}

type SyscallWriteCloser struct {
	fd int
}

func (p *SyscallWriteCloser) Write(data []byte) (int, error) {
	return syscall.Write(p.fd, data)
}

func (p *SyscallWriteCloser) Close() error {
	return syscall.Close(p.fd)
}
