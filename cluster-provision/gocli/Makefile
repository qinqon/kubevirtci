all: fmt build

build: fmt
	bazel run //:gazelle
	bazel build //:cli

fmt:
	go fmt ./cmd/...
	go fmt ./docker/...

container: fmt
	bazel build //:gocli

container-run: fmt
	bazel run //:gocli

push: fmt
	bazel run //:push-all

generate:
	dep ensure
	bazel run //:gazelle
