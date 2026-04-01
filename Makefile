# Image URL to use all building/pushing image targets
DOCKER_TAG                ?= latest
DOCKER_SERVICE_IMAGE_PATH ?= docker.lp.rsint.net/platform
DOCKER_SERVICE_IMAGE_NAME  = ${DOCKER_SERVICE_IMAGE_PATH}/pg-topology-operator
DOCKER_IMAGE              ?= ${DOCKER_SERVICE_IMAGE_NAME}:${DOCKER_TAG}
HELM_REPO_PATH            ?= https://helm.lp.rsint.net/helm-dev/rs/coc/platform

all: test

.PHONY: run
run: fmt vet
	go run main.go

.PHONY: test
test: build
	go test ./... -coverprofile cover.out

.PHONY: build
build: fmt vet
	rm -f bin/operator
	go build -o bin/operator main.go

.PHONY: fmt
fmt: mod
	go fmt . ./pkg/...

.PHONY: vet
vet:
	go vet ./pkg/...

.PHONY: mod
mod:
	go mod tidy

#
# - - - - - - - - - - Artifact Generation - - - - - - - - - -
#

.PHONY: docker-build
docker-build:
	docker build -f Dockerfile -t ${DOCKER_IMAGE} .

.PHONY: docker-upload
docker-upload: docker-build
	docker push ${DOCKER_IMAGE}

.PHONY: helm-package
helm-package:
	helm package helm

.PHONY: helm-upload
helm-upload: helm-package
	chart=$$(ls -t1 pg*.tgz | head -n1); \
	echo "uploading chart $$chart..."; \
	curl -k -u ${AD_USER}:${AD_PASSWORD} -XPUT "${HELM_REPO_PATH}/$$chart" -T $$chart

#
# - - - - - - - - - - K8s Testing - - - - - - - - - -
#

.PHONY: k8s-install
k8s-install: docker-upload
	helm install pg-topology helm -n cnpg-system
