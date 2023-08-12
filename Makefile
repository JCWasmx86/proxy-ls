all:
	cd proxy-ls && go build -v
install: all
	cd proxy-ls && cp proxy-ls /usr/local/bin/proxy-ls
