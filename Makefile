# Image URL to use all building/pushing image targets
DOCKER_TAG                      ?= latest
BUILD_DIR                        = build
DOCKER_SERVICE_IMAGE_PATH       ?= docker.lp.rsint.net/platform
DOCKER_SERVICE_IMAGE_NAME        = ${DOCKER_SERVICE_IMAGE_PATH}/pg-topology-operator
DOCKER_IMAGE ?= ${DOCKER_SERVICE_IMAGE_NAME}:${DOCKER_TAG}
HELM_REPO_PATH ?= https://artifactory.lp.rsint.net/artifactory/platform-helm-dev
HELM_CHART_DIR = ${BUILD_DIR}

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
	docker build -t ${DOCKER_IMAGE} .

.PHONY: docker-upload
docker-upload: docker-build
	docker push ${DOCKER_IMAGE}

.PHONY: helm-package
helm-package:
	helm package helm -d build

.PHONY: helm-upload
helm-upload: ${HELM_CHART_DIR}/*.tgz  # requires helm-package
	@$(foreach path, $^, curl -k -u ${HELM_REPOSITORY_USERNAME}:${HELM_REPOSITORY_PASSWORD} --fail -XPUT "${HELM_REPO_PATH}/$(notdir ${path})" --upload-file ${path} &&) \
		echo "\n\nHelm charts uploaded."

#
# - - - - - - - - - - K8s Testing - - - - - - - - - -
#

.PHONY: k8s-install
k8s-install: docker-upload
	helm install pg-topology helm -n cnpg-system
