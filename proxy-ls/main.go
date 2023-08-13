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

	"github.com/hashicorp/go-set"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/withmandala/go-log"
)

const (
	LanguageServerFactor = 1000000
	PendingRequestsSize  = 5
	FlatpakManifestSize  = 2
	LanguageServerCount  = 3
	DefaultTabSize       = 2
	YamlID               = 1
	JSONID               = 2
	XMLID                = 3
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

type Server struct {
	logger           *log.Logger
	jsonrpc          *JSONRPC
	mu               sync.RWMutex
	jsonLS           *ProcessIO
	xmlLS            *ProcessIO
	yamlLS           *ProcessIO
	jsonrpcs         map[string]*JSONRPC
	initialized      map[string]bool
	diagnostics      map[protocol.URI]([]protocol.Diagnostic)
	pendingRequests  *set.Set[int]
	flatpakManifests *set.Set[string]
}

func AddProcess(command string) *ProcessIO {
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

func NewServer(jsonrpc *JSONRPC) *Server {
	server := &Server{
		logger:           log.New(os.Stderr),
		jsonrpc:          jsonrpc,
		diagnostics:      make(map[protocol.URI]([]protocol.Diagnostic)),
		pendingRequests:  set.New[int](PendingRequestsSize),
		flatpakManifests: set.New[string](FlatpakManifestSize),
		jsonLS:           AddProcess("vscode-json-languageserver --stdio"),
		xmlLS:            AddProcess("lemminx"),
		yamlLS:           AddProcess("yaml-language-server --stdio"),
		mu:               sync.RWMutex{},
		jsonrpcs:         make(map[string]*JSONRPC, LanguageServerCount),
		initialized:      make(map[string]bool, LanguageServerCount),
	}
	go server.setupLS(server.yamlLS, "yaml")
	go server.setupLS(server.jsonLS, "json")
	go server.setupLS(server.xmlLS, "xml")

	return server
}

func (s *Server) setupLS(p *ProcessIO, id string) {
	jsonrpc := &JSONRPC{
		in:  p.stdout,
		out: p.stdin,
	}
	s.mu.Lock()
	s.jsonrpcs[id] = jsonrpc
	s.mu.Unlock()

	for {
		messageData, err := jsonrpc.ReadMessage()
		if err != nil {
			s.logger.Infof("(%v) Error reading message: %s\n", id, err)

			return
		}

		var request map[string]interface{}
		if err := json.Unmarshal(messageData, &request); err != nil {
			s.logger.Infof("(%v) Error decoding request: %s\n", id, err)

			return
		}

		if _, ok := request["id"]; ok {
			s.handleLSResponse(request, jsonrpc, id)
		} else {
			s.handleLSNotification(request, jsonrpc, id)
		}
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
	}

	panic(id)
}

func (s *Server) handleLSResponse(request map[string]interface{}, rpc *JSONRPC, id string) {
	s.logger.Infof("Response: (%v) %d", id, ExtractIntValue(request["id"]))

	if _, ok := request["error"]; ok {
		s.logger.Warnf("Received error from %v: %v", id, request["error"])
	}

	if _, ok := request["params"]; ok {
		stringified, _ := json.Marshal(request["params"])

		if id != "xml" {
			return
		}

		method, ok := request["method"].(string)
		checkok(ok)

		if method == "client/registerCapability" {
			call := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result":  nil,
			}
			data, _ := json.Marshal(call)
			checkerror(s.jsonrpcs[id].SendMessage(data))

			return
		}

		if method != "workspace/configuration" {
			s.logger.Warnf("Unable to handle %s with params %s", method, stringified)

			return
		}

		s.logger.Infof("Querying config...: %s", stringified)

		requested, ok := request["params"].(map[string]interface{})["items"].([]interface{})
		checkok(ok)

		var returned []interface{}

		for _, item := range requested {
			section, ok := item.(map[string]interface{})["section"].(string)
			checkok(ok)

			switch section {
			case "xml.format.insertSpaces":
				returned = append(returned, true)
			case "xml.format.tabSize":
				returned = append(returned, DefaultTabSize)
			default:
				returned = append(returned, nil)
			}
		}

		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  returned,
		}
		data, _ := json.Marshal(call)
		checkerror(s.jsonrpcs[id].SendMessage(data))

		return
	}

	seqID := ExtractIntValue(request["id"])
	if seqID == 1 {
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "initialized",
			"params":  map[string]interface{}{},
		}
		data, _ := json.Marshal(call)
		checkerror(s.jsonrpcs[id].SendMessage(data))
		s.mu.Lock()
		s.initialized[id] = true
		s.mu.Unlock()

		return // Initialization succeeded
	}

	if s.pendingRequests.Contains(seqID) {
		realSeqID := seqID - (str2int(id) * LanguageServerFactor)
		request["id"] = realSeqID
		data, _ := json.Marshal(request)
		checkerror(s.jsonrpc.SendMessage(data))
		s.mu.Lock()
		s.pendingRequests.Remove(seqID)
		s.mu.Unlock()

		return
	}

	data, _ := json.Marshal(request)
	checkerror(s.jsonrpc.SendMessage(data))
}

func (s *Server) publishDiagnostics() {
	for uri, diagnostics := range s.diagnostics {
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params": protocol.PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: []protocol.Diagnostic{},
			},
		}
		data, _ := json.Marshal(call)
		checkerror(s.jsonrpc.SendMessage(data))

		call = map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params": protocol.PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: diagnostics,
			},
		}
		data, _ = json.Marshal(call)
		checkerror(s.jsonrpc.SendMessage(data))
	}
}

func (s *Server) handleLSNotification(request map[string]interface{}, _ *JSONRPC, id string) {
	s.logger.Infof("LS-Notification: (%s) %v", id, request["method"])
	method, ok := request["method"].(string)
	checkok(ok)

	marshalledParams, _ := json.Marshal(request["params"])

	if method == "textDocument/publishDiagnostics" {
		var diags protocol.PublishDiagnosticsParams

		checkerror(json.Unmarshal(marshalledParams, &diags))
		s.diagnostics[diags.URI] = diags.Diagnostics
		s.publishDiagnostics()
	}
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

func (s *Server) InitializeAll(rootURI *string, clientCaps protocol.ClientCapabilities) {
	for _, element := range s.jsonrpcs {
		traceValue := protocol.TraceValueVerbose
		version := "0.0.1"
		call := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": protocol.InitializeParams{
				RootURI: rootURI,
				Trace:   &traceValue,
				ClientInfo: &struct {
					Name    string  `json:"name"`
					Version *string `json:"version,omitempty"`
				}{Name: "proxy-ls", Version: &version},
				Capabilities: clientCaps,
				InitializationOptions: map[string]interface{}{
					"handledSchemaProtocols": []string{"file", "http", "https"},
					"provideFormatter":       true,
				},
			},
		}
		data, _ := json.Marshal(call)
		checkerror(element.SendMessage(data))
	}

	for {
		s.mu.Lock()
		if s.initialized["yaml"] && s.initialized["json"] && s.initialized["xml"] {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

func (s *Server) redirectRequest(id string, request map[string]interface{}) {
	newSeq := ExtractIntValue(request["id"]) + (LanguageServerFactor * str2int(id))
	s.logger.Infof("Redirecting %v to %v as new ID %v", request["method"], id, newSeq)
	request["id"] = newSeq
	data, _ := json.Marshal(request)

	s.mu.Lock()
	s.pendingRequests.Insert(newSeq)
	s.mu.Unlock()
	checkerror(s.jsonrpcs[id].SendMessage(data))
}

func (s *Server) handleCall(request map[string]interface{}) {
	serviceMethod, ok := request["method"].(string)
	checkok(ok)

	seq := request["id"]
	marshalledParams, _ := json.Marshal(request["params"])

	s.logger.Infof("Got call %v", serviceMethod)

	var response interface{}

	switch serviceMethod {
	case "initialize":
		var params protocol.InitializeParams

		checkerror(json.Unmarshal(marshalledParams, &params))
		s.logger.Infof("Client caps: %v", params.Capabilities)
		s.InitializeAll(params.RootURI, params.Capabilities)

		syncType := protocol.TextDocumentSyncKindFull
		version := "0.0.1"
		serverCaps := protocol.InitializeResult{
			Capabilities: protocol.ServerCapabilities{
				TextDocumentSync: &syncType,
				CompletionProvider: &protocol.CompletionOptions{
					TriggerCharacters: []string{",", ".", ":", "_", "-"},
				},
				HoverProvider:              true,
				DefinitionProvider:         true,
				DocumentSymbolProvider:     true,
				CodeActionProvider:         true,
				DocumentFormattingProvider: true,
			},
			ServerInfo: &protocol.InitializeResultServerInfo{
				Name:    "proxy-ls",
				Version: &version,
			},
		}
		response = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      seq,
			"result": map[string]interface{}{
				"capabilities": serverCaps,
				"serverInfo": map[string]interface{}{
					"name":    "proxy-ls",
					"version": "0.1",
				},
			},
		}
	case "textDocument/documentSymbol":
		var params protocol.DocumentSymbolParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectRequest(n, request)
	case "textDocument/formatting":
		var params protocol.DocumentFormattingParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectRequest(n, request)
	case "textDocument/codeAction":
		var params protocol.CodeActionParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectRequest(n, request)
	case "textDocument/completion":
		var params protocol.CompletionParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectRequest(n, request)
	case "textDocument/hover":
		var params protocol.HoverParams
		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectRequest(n, request)
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

	if response == nil {
		return
	}

	responseData, err := json.Marshal(response)
	checkerror(err)

	checkerror(s.jsonrpc.SendMessage(responseData))
}

func (s *Server) selectLSForFile(name string, contents string) string {
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return "yaml"
	} else if strings.HasSuffix(name, ".json") {
		if strings.Contains(contents, "\"build-options\"") && strings.Contains(contents, "\"modules\"") && strings.Contains(contents, "\"finish-args\"") &&
			(strings.Contains(contents, "\"app-id\"") || strings.Contains(contents, "\"id\"")) {
			parts := strings.Split(name, "/")
			s.flatpakManifests.Insert("/" + parts[len(parts)-1])
			s.logger.Infof("Found flatpak manifest %s", parts[len(parts)-1])
		}

		return "json"
	} else if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".doap") {
		return "xml"
	}

	panic(name)
}

func (s *Server) redirectNotification(id string, request map[string]interface{}) {
	s.logger.Infof("Redirecting %v to %v", request["method"], id)
	data, _ := json.Marshal(request)
	checkerror(s.jsonrpcs[id].SendMessage(data))
}

func (s *Server) updateConfigs() {
	s.mu.Lock()
	schemas := [](map[string]interface{}){
		map[string]interface{}{
			"uri":       "https://raw.githubusercontent.com/flatpak/flatpak-builder/main/data/flatpak-manifest.schema.json",
			"fileMatch": s.flatpakManifests.Slice(),
		},
	}
	call := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "json/schemaAssociations",
		"params":  []any{schemas},
	}
	data, _ := json.Marshal(call)
	s.logger.Infof("json/schemaAssociations: %s", string(data))
	checkerror(s.jsonrpcs["json"].SendMessage(data))
	s.mu.Unlock()
}

func (s *Server) handleNotification(request map[string]interface{}) {
	// Handle notifications here if needed
	serviceMethod, ok := request["method"].(string)
	checkok(ok)
	s.logger.Infof("Received notification %s", serviceMethod)

	marshalledParams, _ := json.Marshal(request["params"])

	switch serviceMethod {
	case "textDocument/didOpen":
		var params protocol.DidOpenTextDocumentParams
		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, params.TextDocument.Text)
		s.redirectNotification(n, request)

		s.updateConfigs()
	case "textDocument/didChange":
		var params protocol.DidChangeTextDocumentParams
		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectNotification(n, request)
	case "textDocument/didSave":
		var params protocol.DidSaveTextDocumentParams
		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectNotification(n, request)
	case "textDocument/didClose":
		var params protocol.DidCloseTextDocumentParams
		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "")
		s.redirectNotification(n, request)
	}
}

func (s *Server) Serve() {
	for {
		messageData, err := s.jsonrpc.ReadMessage()
		if err != nil {
			s.logger.Infof("(server<->editor): Error reading message: %s\n", err)

			return
		}

		var request map[string]interface{}
		if err := json.Unmarshal(messageData, &request); err != nil {
			s.logger.Infof("Error decoding request: %s\n", err)

			return
		}

		if _, ok := request["error"]; ok {
			s.logger.Warnf("Received error from editor: %v", request["error"])

			continue
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
