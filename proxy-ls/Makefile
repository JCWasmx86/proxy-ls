proxy-ls: $(wildcard *.go)
	go build -v
install:
	install -D -m755 proxy-ls $(PREFIX)/bin/proxy-ls
format:
	go fmt
