package main

import (
	"io"
	"os"
	"os/exec"
)

type ProcessIO struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func CreateProcessFromCommand(command string) *ProcessIO {
	cmd := exec.Command("bash", "-c", command)
	cmd.Stderr = os.Stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()

	processIO := &ProcessIO{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}

	go func(p *ProcessIO) {
		defer p.Close()
		checkerror(cmd.Start())
		checkerror(cmd.Wait())
	}(processIO)

	return processIO
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
