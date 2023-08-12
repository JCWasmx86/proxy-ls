package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/sourcegraph/go-lsp"
	"github.com/withmandala/go-log"
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

type JSONRPC struct {
	in  io.ReadCloser
	out io.WriteCloser
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

func NewJSONRPC() *JSONRPC {
	real_sout, _ := syscall.Dup(1 /*STDOUT_FILENO*/)
	syscall.Dup2(2 /*STDERR_FILENO*/, 1 /*STDOUT_FILENO*/)
	return &JSONRPC{
		in: os.Stdin,
		out: &SyscallWriteCloser{
			fd: real_sout,
		},
	}
}

func (rpc *JSONRPC) ReadMessage() ([]byte, error) {
	var contentLength int
	var state int
	// Read headers
	tmpData := make([]byte, 1)
	header := ""
	for {
		_, err := rpc.in.Read(tmpData)
		if err != nil {
			return nil, err
		}
		if tmpData[0] == '\r' {
			if state == 2 {
				state = 3
			} else {
				state = 1
			}
		} else if tmpData[0] == '\n' {
			if state == 3 {
				state = 4
				break
			} else {
				state = 2
				if strings.HasPrefix(header, "Content-Length:") {
					number_as_str := strings.TrimSpace(strings.Split(header, ":")[1])
					contentLength, _ = strconv.Atoi(number_as_str)
				}
				fmt.Fprintf(os.Stderr, "Header = %s\n", header)
				header = ""
			}
		} else {
			header += string(tmpData)
			fmt.Fprintf(os.Stderr, ">> Header = %s\n", header)
			state = 5
		}
	}

	// Read JSON-RPC message
	messageData := make([]byte, contentLength)
	_, err := rpc.in.Read(messageData)
	if err != nil {
		return nil, fmt.Errorf("error reading message data: %s", err)
	}

	return messageData, nil
}

func (rpc *JSONRPC) SendMessage(message []byte) error {
	contentLength := len(message)
	headers := fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLength)

	// Write headers and JSON-RPC message
	_, err := rpc.out.Write([]byte(headers))
	if err != nil {
		return fmt.Errorf("error writing headers: %s", err)
	}

	_, err = rpc.out.Write(message)
	if err != nil {
		return fmt.Errorf("error writing message: %s", err)
	}

	return nil
}

type Server struct {
	logger               *log.Logger
	jsonrpc              *JSONRPC
	mu                   sync.Mutex
	json_language_server *ProcessIO
	xml_language_server  *ProcessIO
	yaml_language_server *ProcessIO
	jsonrpcs             [3]*JSONRPC
	initialized          [3]bool
	diagnostics          map[lsp.DocumentURI]([]lsp.Diagnostic)
}

func (s *Server) AddProcess(command string) (*ProcessIO, error) {
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
		cmd.Start()
		cmd.Wait()
	}(processIO)

	return processIO, nil
}

func NewServer(jsonrpc *JSONRPC) *Server {
	server := &Server{
		logger:      log.New(os.Stderr),
		jsonrpc:     jsonrpc,
		diagnostics: make(map[lsp.DocumentURI]([]lsp.Diagnostic)),
	}
	json_ls, err := server.AddProcess("vscode-json-languageserver --stdio")
	if err != nil {
		_ = fmt.Errorf("failed to start vscode-json-languageserver: %v", err)
	}
	server.json_language_server = json_ls
	xml_ls, err := server.AddProcess("lemminx")
	if err != nil {
		_ = fmt.Errorf("failed to start lemminx: %v", err)
	}
	server.xml_language_server = xml_ls
	yaml_ls, err := server.AddProcess("yaml-language-server --stdio")
	if err != nil {
		_ = fmt.Errorf("failed to start yaml-language-server: %v", err)
	}
	server.yaml_language_server = yaml_ls

	go server.setupLS(server.yaml_language_server, 1)
	go server.setupLS(server.json_language_server, 2)
	go server.setupLS(server.xml_language_server, 3)

	return server
}

func (s *Server) setupLS(p *ProcessIO, id int) {
	jsonrpc := &JSONRPC{
		in:  p.stdout,
		out: p.stdin,
	}
	s.jsonrpcs[id-1] = jsonrpc
	for {
		messageData, err := jsonrpc.ReadMessage()
		if err != nil {
			s.logger.Infof("(%d) Error reading message: %s\n", id, err)
			return
		}

		var request map[string]interface{}
		if err := json.Unmarshal(messageData, &request); err != nil {
			s.logger.Infof("(%d) Error decoding request: %s\n", id, err)
			return
		}

		if _, ok := request["id"]; ok {
			s.handleLSResponse(request, jsonrpc, id)
		} else {
			s.handleLSNotification(request, jsonrpc, id)
		}
	}
}

func (s *Server) handleLSResponse(request map[string]interface{}, rpc *JSONRPC, id int) {
	s.logger.Infof("Response: (%d) %v", id, request)
	if ExtractIntValue(request["id"], 1) {
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "initialized",
			"params":  map[string]interface{}{},
		}
		data, _ := json.Marshal(call)
		s.jsonrpcs[id-1].SendMessage(data)
		s.initialized[id-1] = true
		return // Initialization succeeded
	}
}

func (s *Server) publishDiagnostics() {
	for k, v := range s.diagnostics {
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params": lsp.PublishDiagnosticsParams{
				URI:         k,
				Diagnostics: []lsp.Diagnostic{},
			},
		}
		data, _ := json.Marshal(call)
		s.jsonrpc.SendMessage(data)
		call = map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params": lsp.PublishDiagnosticsParams{
				URI:         k,
				Diagnostics: v,
			},
		}
		data, _ = json.Marshal(call)
		s.jsonrpc.SendMessage(data)
	}
}

func (s *Server) handleLSNotification(request map[string]interface{}, rpc *JSONRPC, id int) {
	s.logger.Infof("LS-Notification: (%d) %v", id, request)
	method := request["method"].(string)
	marshalled_params, _ := json.Marshal(request["params"])
	switch method {
	case "textDocument/publishDiagnostics":
		var diags lsp.PublishDiagnosticsParams
		json.Unmarshal(marshalled_params, &diags)
		s.diagnostics[diags.URI] = diags.Diagnostics
		s.publishDiagnostics()
	}
}

func ExtractIntValue(idValue interface{}, n int) bool {
	switch id := idValue.(type) {
	case float64:
		return id == float64(n)
	case string:
		return id == fmt.Sprint(n)
	case int:
		return id == n
	default:
		return false
	}
}
func (s *Server) InitializeAll(rootURI lsp.DocumentURI, clientCaps lsp.ClientCapabilities) {
	for _, element := range s.jsonrpcs {
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": lsp.InitializeParams{
				RootURI:      rootURI,
				Trace:        "verbose",
				ClientInfo:   lsp.ClientInfo{Name: "proxy-ls", Version: "0.0.1"},
				Capabilities: clientCaps,
			},
		}
		data, _ := json.Marshal(call)
		element.SendMessage(data)
	}
	for {
		if s.initialized[0] && s.initialized[1] && s.initialized[2] {
			return
		}
	}
}

func (s *Server) handleCall(request map[string]interface{}) {
	serviceMethod := request["method"].(string)
	seq := request["id"]
	marshalled_params, _ := json.Marshal(request["params"])
	s.logger.Infof("Got call %v", serviceMethod)

	var response interface{}

	switch serviceMethod {
	case "initialize":
		var initParams lsp.InitializeParams
		json.Unmarshal(marshalled_params, &initParams)
		s.InitializeAll(initParams.RootURI, initParams.Capabilities)
		sync_type := lsp.TDSKFull
		server_caps := lsp.InitializeResult{
			Capabilities: lsp.ServerCapabilities{
				TextDocumentSync: &lsp.TextDocumentSyncOptionsOrKind{
					Kind: &sync_type,
				},
				CompletionProvider: &lsp.CompletionOptions{
					TriggerCharacters: []string{",", ".", ":", "_", "-"},
				},
				HoverProvider:              true,
				DefinitionProvider:         true,
				DocumentSymbolProvider:     true,
				CodeActionProvider:         true,
				DocumentFormattingProvider: true,
			},
		}
		response = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      seq,
			"result": map[string]interface{}{
				"capabilities": server_caps,
				"serverInfo": map[string]interface{}{
					"name":    "proxy-ls",
					"version": "0.1",
				},
			},
		}
	default:
		response = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      seq,
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "Method not found",
			},
		}
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		s.logger.Infof("Error encoding response: %s\n", err)
		return
	}

	err = s.jsonrpc.SendMessage(responseData)
	if err != nil {
		s.logger.Infof("Error sending response: %s\n", err)
		return
	}
}

func (s *Server) selectLSForFile(name string, contents string) int {
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return 1
	} else if strings.HasSuffix(name, ".json") {
		return 2
	} else if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".doap") {
		return 3
	}
	return 0
}

func (s *Server) redirectNotification(n int, request map[string]interface{}, uri lsp.DocumentURI) {
	s.logger.Infof("Redirecting %s to %v", request["method"].(string), n)
	data, _ := json.Marshal(request)
	s.jsonrpcs[n-1].SendMessage(data)
	if n == 2 { // JSON
		s.logger.Info("Doing another didChangeWatchedFiles notification")
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "workspace/didChangeWatchedFiles",
			"params": lsp.DidChangeWatchedFilesParams{
				Changes: []lsp.FileEvent{
					{
						URI:  uri,
						Type: lsp.Changed,
					},
				},
			},
		}
		data, _ := json.Marshal(call)
		s.jsonrpcs[n-1].SendMessage(data)
		s.initialized[n-1] = true
	}
}

func (s *Server) handleNotification(request map[string]interface{}) {
	// Handle notifications here if needed
	serviceMethod := request["method"].(string)
	s.logger.Infof("Received notification %s", serviceMethod)
	marshalled_params, _ := json.Marshal(request["params"])

	switch serviceMethod {
	case "textDocument/didOpen":
		var initParams lsp.DidOpenTextDocumentParams
		json.Unmarshal(marshalled_params, &initParams)
		s.logger.Infof("didOpen: %v", initParams)
		n := s.selectLSForFile(string(initParams.TextDocument.URI), initParams.TextDocument.Text)
		if n != 0 {
			s.redirectNotification(n, request, initParams.TextDocument.URI)
		}
	case "textDocument/didChange":
		var initParams lsp.DidChangeTextDocumentParams
		json.Unmarshal(marshalled_params, &initParams)
		n := s.selectLSForFile(string(initParams.TextDocument.URI), initParams.ContentChanges[0].Text)
		if n != 0 {
			s.redirectNotification(n, request, initParams.TextDocument.URI)
		}
	case "textDocument/didSave":
		var initParams lsp.DidSaveTextDocumentParams
		json.Unmarshal(marshalled_params, &initParams)
		n := s.selectLSForFile(string(initParams.TextDocument.URI), "")
		if n != 0 {
			s.redirectNotification(n, request, initParams.TextDocument.URI)
		}
	case "textDocument/didClose":
		var initParams lsp.DidCloseTextDocumentParams
		json.Unmarshal(marshalled_params, &initParams)
		n := s.selectLSForFile(string(initParams.TextDocument.URI), "")
		if n != 0 {
			s.redirectNotification(n, request, initParams.TextDocument.URI)
		}
	}
}

func (s *Server) Serve() {
	for {
		messageData, err := s.jsonrpc.ReadMessage()
		if err != nil {
			s.logger.Infof("Error reading message: %s\n", err)
			return
		}

		var request map[string]interface{}
		if err := json.Unmarshal(messageData, &request); err != nil {
			s.logger.Infof("Error decoding request: %s\n", err)
			return
		}

		if _, ok := request["id"]; ok {
			s.handleCall(request)
		} else {
			s.handleNotification(request)
		}
	}
}

func main() {
	rpc := NewJSONRPC()
	server := NewServer(rpc)
	server.Serve()
}
