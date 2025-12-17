# Image URL to use all building/pushing image targets
DOCKER_IMAGE ?= docker.lp.rsint.net/platform/pg-topology-operator:latest

all: test

run: fmt vet
	go run main.go

test: build
	go test ./... -coverprofile cover.out

build: fmt vet
	rm -f bin/operator
	go build -o bin/operator main.go

docker-build: fmt vet
	docker build -t ${DOCKER_IMAGE} .

docker-push: docker-build
	docker push ${DOCKER_IMAGE}

install: docker-push
	helm install pg-topology helm -n cnpg-system

helm-package:
	helm package helm -d build

helm-push: helm-package
	helm push build/* helm.com-dev01.lp.rsint.net

fmt: mod
	go fmt . ./pkg/...

vet:
	go vet ./pkg/...

mod:
	go mod tidy
