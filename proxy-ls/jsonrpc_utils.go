package main

func make_notification(method string, params any) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
}
