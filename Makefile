.PHONY: dev build install image release profile bench test clean

CGO_ENABLED=0
VERSION=$(shell git describe --abbrev=0 --tags)
COMMIT=$(shell git rev-parse --short HEAD)

all: dev

dev: build
	@./bitraft --version

build: clean
	@go build \
		-tags "netgo static_build" -installsuffix netgo \
		-ldflags "-w -X $(shell go list).Version=$(VERSION) -X $(shell go list).Commit=$(COMMIT)" \
		.

install: build
	@go install .

image:
	@docker build -t prologic/bitraft .

release:
	@./tools/release.sh

profile: build
	@go test -cpuprofile cpu.prof -memprofile mem.prof -v -bench ./...

bench: build
	@go test -v -benchmem -bench=. ./...

test: build
	@go test -v -cover -coverprofile=coverage.txt -covermode=atomic -coverpkg=$(shell go list) -race ./...

clean:
	@git clean -f -d -X
