package main

func make_notification(method string, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
}

func make_response(seq any, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      seq,
		"result":  params,
	}
}

func make_request(seq any, method string, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      seq,
		"method":  method,
		"params":  params,
	}
}
