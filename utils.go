package main

import (
	"strconv"
	"syscall"
)

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

func ExtractIntValue(idValue interface{}) int {
	switch value := idValue.(type) {
	case float64:
		return int(value)
	case string:
		r, err := strconv.Atoi(value)
		checkerror(err)

		return r
	case int:
		return value
	default:
		panic(value)
	}
}

func str2int(id string) int {
	switch id {
	case "yaml":
		return YamlID
	case "json":
		return JSONID
	case "xml":
		return XMLID
	case "ruff":
		return RUFFID
	case "rome":
		return ROMEID
	}

	panic(id)
}
