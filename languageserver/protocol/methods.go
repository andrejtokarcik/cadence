package protocol

import "encoding/json"

func (server *Server) handleInitialize(req *json.RawMessage) (interface{}, error) {
	var params InitializeParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	return server.Handler.Initialize(server.conn, &params)
}

func (server *Server) handleDidOpenTextDocument(req *json.RawMessage) (interface{}, error) {
	var params DidOpenTextDocumentParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	err := server.Handler.DidOpenTextDocument(server.conn, &params)
	return nil, err
}

func (server *Server) handleDidChangeTextDocument(req *json.RawMessage) (interface{}, error) {
	var params DidChangeTextDocumentParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	err := server.Handler.DidChangeTextDocument(server.conn, &params)
	return nil, err
}

func (server *Server) handleHover(req *json.RawMessage) (interface{}, error) {
	var params TextDocumentPositionParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	return server.Handler.Hover(server.conn, &params)
}

func (server *Server) handleDefinition(req *json.RawMessage) (interface{}, error) {
	var params TextDocumentPositionParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	return server.Handler.Definition(server.conn, &params)
}

func (server *Server) handleSignatureHelp(req *json.RawMessage) (interface{}, error) {
	var params TextDocumentPositionParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	return server.Handler.SignatureHelp(server.conn, &params)
}

func (server *Server) handleCodeLens(req *json.RawMessage) (interface{}, error) {
	var params CodeLensParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	return server.Handler.CodeLens(server.conn, &params)
}

func (server *Server) handleExecuteCommand(req *json.RawMessage) (interface{}, error) {
	var params ExecuteCommandParams
	if err := json.Unmarshal(*req, &params); err != nil {
		return nil, err
	}

	return server.Handler.ExecuteCommand(server.conn, &params)
}

func (server *Server) handleShutdown(_ *json.RawMessage) (interface{}, error) {
	err := server.Handler.Shutdown(server.conn)
	return nil, err
}

func (server *Server) handleExit(_ *json.RawMessage) (interface{}, error) {
	err := server.Handler.Exit(server.conn)
	return nil, err
}