# Image URL to use all building/pushing image targets
DOCKER_TAG                ?= latest
DOCKER_REGISTRY           ?= docker.lp.rsint.net
DOCKER_SERVICE_IMAGE_PATH ?= ${DOCKER_REGISTRY}/platform
DOCKER_SERVICE_IMAGE_NAME  = ${DOCKER_SERVICE_IMAGE_PATH}/pg-topology-operator
DOCKER_IMAGE              ?= ${DOCKER_SERVICE_IMAGE_NAME}:${DOCKER_TAG}
HELM_REPO_PATH            ?= https://helm.lp.rsint.net/helm-dev/rs/coc/platform

all: test

.PHONY: run
run: fmt vet
	go run main.go

.PHONY: test
test: build crds envtest
	@mkdir -p coverage
	KUBEBUILDER_ASSETS="$$( $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path )" \
	go test -race -p 1 ./... -coverprofile coverage/cover.out

coverage: test
	go tool cover -html=coverage/cover.out -o coverage/coverage.html
	@echo "Coverage report: coverage/coverage.html"

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
	docker login ${DOCKER_REGISTRY} --username ${AD_USER} --password ${AD_PASSWORD}
	docker push ${DOCKER_IMAGE}

.PHONY: helm-package
helm-package:
	helm package helm

.PHONY: helm-upload
helm-upload: helm-package
	chart=$$(ls -t1 pg*.tgz | head -n1); \
	echo "uploading chart $$chart..."; \
	curl -k -u ${AD_USER}:${AD_PASSWORD} -XPUT "${HELM_REPO_PATH}/$$chart" -T $$chart

# --- ENVTEST INTEGRATION LAYER ---
ENVTEST_K8S_VERSION = 1.28.0
LOCALBIN ?= $(shell pwd)/bin
ENVTEST ?= $(LOCALBIN)/setup-envtest
CRD_DIR ?= config/crd/bases

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST):
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: crds
crds:
	@mkdir -p $(CRD_DIR)
	@if [ ! -f $(CRD_DIR)/cnpg.yaml ]; then \
		echo "Downloading CloudNativePG schema definitions (v1.28.1) for envtest API validation..."; \
		curl -sLo $(CRD_DIR)/cnpg.yaml https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/v1.28.1/releases/cnpg-1.28.1.yaml; \
	fi

# --- HELM TEMPLATE TESTS ---
# Runs helm-unittest against the helm/ chart directory.
# The plugin is auto-installed if not already present.
.PHONY: helm-test
helm-test:
	@helm plugin list | grep -q unittest || \
		helm plugin install https://github.com/helm-unittest/helm-unittest --verify=false
	helm unittest helm/

#
# - - - - - - - - - - K8s Testing - - - - - - - - - -
#

.PHONY: k8s-install
k8s-install: docker-upload
	helm install pg-topology helm -n cnpg-system
