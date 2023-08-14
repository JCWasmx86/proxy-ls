PREFIX ?= /usr/local
MMAKE = PREFIX=$(PREFIX) $(MAKE)
all:
	cd proxy-ls && $(MMAKE)
install: all
	cd proxy-ls && $(MMAKE) install
format:
	cd proxy-ls && $(MMAKE) format
