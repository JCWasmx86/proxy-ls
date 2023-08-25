package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/hashicorp/go-set"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/withmandala/go-log"
)

type Server struct {
	logger               *log.Logger
	jsonrpc              *JSONRPC
	mu                   sync.RWMutex
	jsonLS               *ProcessIO
	xmlLS                *ProcessIO
	yamlLS               *ProcessIO
	jsonrpcs             map[string]*JSONRPC
	initialized          map[string]bool
	diagnostics          map[protocol.URI]([]protocol.Diagnostic)
	pendingRequests      *set.Set[int]
	flatpakManifests     *set.Set[string]
	yamlFlatpakManifests *set.Set[string]
	gschemaFiles         *set.Set[string]
	gresourceFiles       *set.Set[string]
}

func NewServer(jsonrpc *JSONRPC) *Server {
	server := &Server{
		logger:               log.New(os.Stderr),
		jsonrpc:              jsonrpc,
		diagnostics:          make(map[protocol.URI]([]protocol.Diagnostic)),
		pendingRequests:      set.New[int](PendingRequestsSize),
		flatpakManifests:     set.New[string](AverageFileCount),
		yamlFlatpakManifests: set.New[string](AverageFileCount),
		gschemaFiles:         set.New[string](AverageFileCount),
		gresourceFiles:       set.New[string](AverageFileCount),
		jsonLS:               CreateProcessFromCommand("vscode-json-languageserver --stdio"),
		xmlLS:                CreateProcessFromCommand("lemminx"),
		yamlLS:               CreateProcessFromCommand("yaml-language-server --stdio"),
		mu:                   sync.RWMutex{},
		jsonrpcs:             make(map[string]*JSONRPC, LanguageServerCount),
		initialized:          make(map[string]bool, LanguageServerCount),
	}
	server.jsonrpcs["yaml"] = jsonrpcFromProcessIO(server.yamlLS)
	server.jsonrpcs["json"] = jsonrpcFromProcessIO(server.jsonLS)
	server.jsonrpcs["xml"] = jsonrpcFromProcessIO(server.xmlLS)
	server.jsonrpcs["ruff"] = jsonrpcFromProcessIO(CreateProcessFromCommand("ruff-lsp"))
	server.jsonrpcs["rome"] = jsonrpcFromProcessIO(CreateProcessFromCommand("rome lsp-proxy"))

	go server.runLS(server.jsonrpcs["yaml"], "yaml")
	go server.runLS(server.jsonrpcs["json"], "json")
	go server.runLS(server.jsonrpcs["xml"], "xml")
	go server.runLS(server.jsonrpcs["ruff"], "ruff")
	go server.runLS(server.jsonrpcs["rome"], "rome")

	return server
}

func (s *Server) runLS(jsonrpc *JSONRPC, id string) {
	for {
		messageData, err := jsonrpc.ReadMessage()
		if err != nil {
			s.logger.Errorf("(%v) Error reading message: %s\n", id, err)

			return
		}

		var request map[string]interface{}
		if err := json.Unmarshal(messageData, &request); err != nil {
			s.logger.Errorf("(%v) Error decoding request: %s\n", id, err)

			return
		}

		if _, ok := request["id"]; ok {
			s.handleLSResponse(request, jsonrpc, id)
		} else {
			s.handleLSNotification(request, jsonrpc, id)
		}
	}
}

func (s *Server) handleLSResponse(request map[string]interface{}, rpc *JSONRPC, id string) {
	s.logger.Infof("Response: (%v) %d", id, ExtractIntValue(request["id"]))

	if _, ok := request["error"]; ok {
		s.logger.Warnf("Received error from %v: %v", id, request["error"])
	}

	if _, ok := request["params"]; ok {
		stringified, _ := json.Marshal(request["params"])

		method, ok := request["method"].(string)
		checkok(ok)

		if method == "client/registerCapability" {
			call := makeResponse(request["id"], nil)
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
			case "yaml":
				schemas := map[string]interface{}{
					"https://raw.githubusercontent.com/flatpak/flatpak-builder/main/data/flatpak-manifest.schema.json": s.yamlFlatpakManifests.Slice(),
				}
				returned = append(returned, yamlConfig(schemas))
			case "[yaml]":
				returned = append(returned, map[string]interface{}{
					"editor.tabSize":      DefaultTabSize,
					"editor.insertSpace":  true,
					"editor.formatOnType": false,
				})
			case "editor":
				returned = append(returned, map[string]interface{}{
					"detectIndentation": true,
				})
			case "files":
				returned = append(returned, map[string]interface{}{})
			case "rome":
				returned = append(returned, map[string]interface{}{
					"unstable":              true,
					"rename":                true,
					"require_configuration": true,
				})
			default:
				s.logger.Warnf("Unable to handle configuration %s from %s", section, id)

				returned = append(returned, nil)
			}
		}

		call := makeResponse(request["id"], returned)
		data, _ := json.Marshal(call)
		s.logger.Infof("Returned config: %s", string(data))
		checkerror(s.jsonrpcs[id].SendMessage(data))

		return
	}

	seqID := ExtractIntValue(request["id"])
	if seqID == 1 {
		call := makeNotification("initialized", map[string]interface{}{})
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
		call := makeNotification("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: []protocol.Diagnostic{},
		})
		data, _ := json.Marshal(call)
		checkerror(s.jsonrpc.SendMessage(data))

		call = makeNotification("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: diagnostics,
		})
		data, _ = json.Marshal(call)
		checkerror(s.jsonrpc.SendMessage(data))
	}
}

func (s *Server) handleLSNotification(request map[string]interface{}, _ *JSONRPC, id string) {
	method, ok := request["method"].(string)
	checkok(ok)

	marshalledParams, _ := json.Marshal(request["params"])

	if method == "textDocument/publishDiagnostics" {
		var diags protocol.PublishDiagnosticsParams

		checkerror(json.Unmarshal(marshalledParams, &diags))
		s.diagnostics[diags.URI] = diags.Diagnostics
		s.publishDiagnostics()
	}

	if method == "window/logMessage" {
		params := request["params"].(map[string]interface{})
		s.logger.Infof("%s: %s", id, params["message"])
	}
}

func (s *Server) InitializeAll(rootURI *string, clientCaps protocol.ClientCapabilities) {
	capability := true
	clientCaps.Workspace.Configuration = &capability
	clientCaps.TextDocument.RangeFormatting = &protocol.DocumentRangeFormattingClientCapabilities{
		DynamicRegistration: &capability,
	}

	for _, element := range s.jsonrpcs {
		traceValue := protocol.TraceValueVerbose
		version := "0.0.1"
		pid := int32(syscall.Getpid())
		call := makeRequest(1, "initialize", protocol.InitializeParams{
			ProcessID: &pid,
			RootURI:   rootURI,
			Trace:     &traceValue,
			ClientInfo: &struct {
				Name    string  `json:"name"`
				Version *string `json:"version,omitempty"`
			}{Name: "proxy-ls", Version: &version},
			Capabilities: clientCaps,
			InitializationOptions: map[string]interface{}{
				"handledSchemaProtocols": []string{"file", "http", "https"},
				"provideFormatter":       true,
				"settings": map[string]interface{}{
					"xml":  xmlConfig(make([](map[string]interface{}), 0)),
					"yaml": yamlConfig(map[string]interface{}{}),
					"pyright": map[string]interface{}{
						"disableOrganizeImports": true, // ruff-lsp does that
					},
					"python": map[string]interface{}{
						"analysis": map[string]interface{}{
							"autoImportCompletions": true,
							"logLevel":              "Trace",
							"typeCheckingMode":      "strict",
						},
					},
				},
				"globalSettings": map[string]interface{}{
					"logLevel":        "debug",
					"run":             "onType",
					"organizeImports": true,
					"fixAll":          true,
					"codeAction": map[string]interface{}{
						"fixViolation": map[string]interface{}{
							"enable": true,
						},
						"disableRuleComment": map[string]interface{}{
							"enable": true,
						},
					},
				},
			},
		})
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
		s.InitializeAll(params.RootURI, params.Capabilities)

		syncType := protocol.TextDocumentSyncKindIncremental
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
		response = makeResponse(seq, map[string]interface{}{
			"capabilities": serverCaps,
			"serverInfo": map[string]interface{}{
				"name":    "proxy-ls",
				"version": "0.1",
			},
		})
	case "textDocument/documentSymbol":
		var params protocol.DocumentSymbolParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/formatting":
		var params protocol.DocumentFormattingParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/codeAction":
		var params protocol.CodeActionParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/completion":
		var params protocol.CompletionParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/hover":
		var params protocol.HoverParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/declaration":
		var params protocol.DeclarationParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/definition":
		var params protocol.DefinitionParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectRequest(n, request)
	case "textDocument/rename":
		var params protocol.RenameParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
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

func (s *Server) selectLSForFile(name string, contents string, skipUpdate bool) string {
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		isFlatpak := strings.Contains(contents, "finish-args:") && strings.Contains(contents, "modules:") &&
			(strings.Contains(contents, "app-id:") || strings.Contains(contents, "id"))
		if isFlatpak {
			parts := strings.Split(name, "/")
			s.yamlFlatpakManifests.Insert(parts[len(parts)-1])
			s.logger.Infof("Found YAML flatpak manifest %s", parts[len(parts)-1])
		}

		if !skipUpdate {
			s.updateConfigs()
		}

		return "yaml"
	} else if strings.HasSuffix(name, ".json") {
		isFlatpak := strings.Contains(contents, "\"build-options\"") && strings.Contains(contents, "\"modules\"") && strings.Contains(contents, "\"finish-args\"") &&
			(strings.Contains(contents, "\"app-id\"") || strings.Contains(contents, "\"id\""))
		if isFlatpak {
			parts := strings.Split(name, "/")
			s.flatpakManifests.Insert(parts[len(parts)-1])
			s.logger.Infof("Found flatpak manifest %s", parts[len(parts)-1])

			if !skipUpdate {
				s.updateConfigs()
			}
		}

		return "json"
	} else if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".doap") {
		if strings.HasSuffix(name, ".gschema.xml") {
			parts := strings.Split(name, "/")
			s.gschemaFiles.Insert(strings.ReplaceAll(name, "file://", ""))
			s.logger.Infof("Found .gschema.xml file %s", parts[len(parts)-1])

			if !skipUpdate {
				s.updateConfigs()
			}
		} else if strings.HasSuffix(name, ".gresource.xml") {
			parts := strings.Split(name, "/")
			s.gresourceFiles.Insert(strings.ReplaceAll(name, "file://", ""))
			s.logger.Infof("Found .gresource.xml file %s", parts[len(parts)-1])

			if !skipUpdate {
				s.updateConfigs()
			}
		}

		return "xml"
	} else if strings.HasSuffix(name, ".py") {
		return "ruff"
	} else if strings.HasSuffix(name, ".js") {
		return "rome"
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
	call := makeNotification("json/schemaAssociations", []any{schemas})
	data, _ := json.Marshal(call)
	s.logger.Infof("json/schemaAssociations: %s", string(data))
	checkerror(s.jsonrpcs["json"].SendMessage(data))

	schemas = [](map[string]interface{}){}
	for _, gschema := range s.gschemaFiles.Slice() {
		schemas = append(schemas, map[string]interface{}{
			"pattern":  gschema,
			"systemId": "https://gitlab.gnome.org/GNOME/glib/-/raw/HEAD/gio/gschema.dtd",
		})
	}
	for _, gresource := range s.gresourceFiles.Slice() {
		schemas = append(schemas, map[string]interface{}{
			"pattern":  gresource,
			"systemId": "https://gitlab.gnome.org/GNOME/glib/-/raw/HEAD/gio/gresource.dtd",
		})
	}

	call = makeNotification("workspace/didChangeConfiguration", map[string]interface{}{
		"settings": map[string]interface{}{
			"xml": xmlConfig(schemas),
		},
	})
	data, _ = json.Marshal(call)
	s.logger.Infof("workspace/didChangeConfiguration: %s", string(data))
	checkerror(s.jsonrpcs["xml"].SendMessage(data))
	yamlSchemas := map[string]interface{}{
		"https://raw.githubusercontent.com/flatpak/flatpak-builder/main/data/flatpak-manifest.schema.json": s.yamlFlatpakManifests.Slice(),
	}
	call = makeNotification("workspace/didChangeConfiguration", map[string]interface{}{
		"yaml": yamlConfig(yamlSchemas),
	})
	data, _ = json.Marshal(call)
	s.logger.Infof("YAML: workspace/didChangeConfiguration: %s", string(data))
	checkerror(s.jsonrpcs["yaml"].SendMessage(data))
	s.mu.Unlock()
}

func (s *Server) handleNotification(request map[string]interface{}) {
	serviceMethod, ok := request["method"].(string)
	checkok(ok)
	s.logger.Infof("Received notification %s", serviceMethod)

	marshalledParams, _ := json.Marshal(request["params"])

	switch serviceMethod {
	case "textDocument/didOpen":
		var params protocol.DidOpenTextDocumentParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, params.TextDocument.Text, false)
		s.redirectNotification(n, request)

		s.updateConfigs()
	case "textDocument/didChange":
		var params protocol.DidChangeTextDocumentParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectNotification(n, request)
	case "textDocument/didSave":
		var params protocol.DidSaveTextDocumentParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
		s.redirectNotification(n, request)
	case "textDocument/didClose":
		var params protocol.DidCloseTextDocumentParams

		checkerror(json.Unmarshal(marshalledParams, &params))

		n := s.selectLSForFile(params.TextDocument.URI, "", true)
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
			s.logger.Errorf("Error decoding request: %s\n", err)

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
