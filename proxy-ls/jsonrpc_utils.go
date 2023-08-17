package main

func makeNotification(method string, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
}

func makeResponse(seq any, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      seq,
		"result":  params,
	}
}

func makeRequest(seq any, method string, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      seq,
		"method":  method,
		"params":  params,
	}
}
