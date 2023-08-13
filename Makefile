all:
	cd proxy-ls && go build -v
install: all
	cd proxy-ls && cp proxy-ls /usr/local/bin/proxy-ls
format:
	cd proxy-ls && go fmt
