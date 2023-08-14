package main

import (
	"io"
	"os/exec"
)

type ProcessIO struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *ProcessIO) Read(data []byte) (int, error) {
	return p.stdout.Read(data)
}

func (p *ProcessIO) Write(data []byte) (int, error) {
	return p.stdin.Write(data)
}

func (p *ProcessIO) Close() error {
	err := p.stdin.Close()
	if err != nil {
		return err
	}

	err = p.stdout.Close()
	if err != nil {
		return err
	}

	return nil
}
