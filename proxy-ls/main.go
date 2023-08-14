package main

func main() {
	rpc := NewJSONRPC()
	server := NewServer(rpc)
	server.Serve()
}
