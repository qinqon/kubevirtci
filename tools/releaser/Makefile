SHELL := /bin/bash


BIN_DIR = $(CURDIR)/build
GO ?= go

export GO111MODULE=on
export GOPROXY=direct
export GOSUMDB=off
export GOFLAGS=-mod=vendor


all: releaaser

.PHONY: releaser
releaser:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build \
				-o $(BIN_DIR)/releaser .

.PHONY: run
run: releaser
	$(BIN_DIR)/releaser $(WITH)

.PHONY: fmt
fmt:
	$(GO) fmt .

.PHONY: vendor
vendor:
	$(GO) mod tidy
	$(GO) mod vendor
