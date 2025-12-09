# Image URL to use all building/pushing image targets
IMG ?= jaconi.io/ipoque-pg-webhook:latest

all: test

run: fmt vet
	go run main.go

test: build
	go test ./... -coverprofile cover.out

build: fmt vet
	rm -f bin/operator
	go build -o bin/operator main.go

docker-build: fmt vet
	docker build -t ${IMG} .

fmt: mod
	go fmt . ./pkg/...

vet:
	go vet ./pkg/...

mod:
	go mod tidy
