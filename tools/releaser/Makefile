SHELL := /bin/bash


BIN_DIR = $(CURDIR)/build
GO ?= go

all: releaaser

.PHONY: releaser
releaser:
	GO111MODULE=on GOARCH=amd64 GOOS=linux CGO_ENABLED=0 GOFLAGS=-mod=vendor $(GO) build -o $(BIN_DIR)/releaser ./cmd/...

.PHONY: test
test :
	$(GO) test ./cmd/... -test.v -ginkgo.v

.PHONY: run
run: releaser
	$(BIN_DIR)/releaser $(WITH)

.PHONY: fmt
fmt:
	$(GO) fmt ./cmd/...

.PHONY: vendor
vendor:
	$(GO) mod tidy
	$(GO) mod vendor
