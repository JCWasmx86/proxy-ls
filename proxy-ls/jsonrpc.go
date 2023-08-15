package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

type JSONRPC struct {
	in  io.ReadCloser
	out io.WriteCloser
}

func NewJSONRPC() *JSONRPC {
	realSout, _ := syscall.Dup(syscall.Stdout)
	checkerror(syscall.Dup2(syscall.Stderr, syscall.Stdout))

	return &JSONRPC{
		in: os.Stdin,
		out: &SyscallWriteCloser{
			fd: realSout,
		},
	}
}

func (rpc *JSONRPC) ReadMessage() ([]byte, error) {
	var contentLength int
	var state int

	// Read headers
	tmpData := make([]byte, 1)
	header := ""
	breakFromLoop := false

	for {
		if breakFromLoop {
			break
		}

		_, err := rpc.in.Read(tmpData)
		if err != nil {
			return nil, err
		}

		/*
		* This is a state machine to parse the headers of a JSON-RPC message in the format:
		* header: value\r\n
		* Content-Length: 50\r\n
		* \r\n
		* {"jsonrpc": "2.0", ....}
		 */

		switch tmpData[0] {
		case '\r':
			if state == 2 {
				state = 3
			} else {
				state = 1
			}
		case '\n':
			if state == 3 {
				breakFromLoop = true

				break
			}

			state = 2

			if strings.HasPrefix(header, "Content-Length:") {
				numberAsStr := strings.TrimSpace(strings.Split(header, ":")[1])
				contentLength, _ = strconv.Atoi(numberAsStr)
			}

			header = ""
		default:
			header += string(tmpData)
			state = 5
		}
	}

	// Read JSON-RPC message
	messageData := make([]byte, contentLength)

	_, err := rpc.in.Read(messageData)
	if err != nil {
		return nil, fmt.Errorf("ReadMessage(): error reading message data: %w", err)
	}

	return messageData, nil
}

func (rpc *JSONRPC) SendMessage(message []byte) error {
	if string(message) == "null" {
		panic(message)
	}

	contentLength := len(message)
	headers := fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLength)

	// Write headers and JSON-RPC message
	_, err := rpc.out.Write([]byte(headers))
	if err != nil {
		return fmt.Errorf("error writing headers: %w", err)
	}

	_, err = rpc.out.Write(message)
	if err != nil {
		return fmt.Errorf("error writing message: %w", err)
	}

	return nil
}

func jsonrpcFromProcessIO(p *ProcessIO) *JSONRPC {
	return &JSONRPC{
		in:  p.stdout,
		out: p.stdin,
	}
}
