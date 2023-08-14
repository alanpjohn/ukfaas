Version := $(shell git describe --tags --dirty)
GitCommit := $(shell git rev-parse HEAD)
LDFLAGS := "-s -w -X main.Version=$(Version) -X main.GitCommit=$(GitCommit)"

export GO111MODULE=on

.PHONY: all
all: build hashgen

.PHONY: publish
publish: build hashgen

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags $(LDFLAGS) -o dist/faasd

.PHONY: hashgen
hashgen:
	for f in dist/faasd*; do shasum -a 256 $$f > $$f.sha256; done

verify-compose:
	@echo Verifying docker-compose.yaml images in remote registries && \
	arkade chart verify --verbose=$(VERBOSE) -f ./docker-compose.yaml