PREFIX ?= /usr/local
proxy-ls: $(wildcard *.go)
	go build -v
install:
	install -D -m755 proxy-ls $(PREFIX)/bin/proxy-ls
format:
	wsl --fix .
	go fmt
	gofumpt -l -w .

