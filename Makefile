GIT_REMOTE     ?= origin
CONTAINER_ENGINE ?= docker
GO_MOD_FLAGS   ?= -mod=vendor

CHANGELOG     := CHANGELOG.md
CHART_FILE    := helm/Chart.yaml
CHART_NAME    := $(shell awk '/^name:/ { print $$2; exit }' $(CHART_FILE))
CHART_VERSION := $(shell awk '/^version:/ { gsub(/"/, "", $$2); print $$2; exit }' $(CHART_FILE))
APP_VERSION   := $(shell awk '/^appVersion:/ { gsub(/"/, "", $$2); print $$2; exit }' $(CHART_FILE))

DOCKER_REGISTRY           ?= docker.lp.rsint.net
DOCKER_SERVICE_IMAGE_PATH ?= ${DOCKER_REGISTRY}/platform
DOCKER_SERVICE_IMAGE_NAME  = ${DOCKER_SERVICE_IMAGE_PATH}/pg-topology-operator
DOCKER_IMAGE              ?= ${DOCKER_SERVICE_IMAGE_NAME}:${APP_VERSION}
HELM_REPO_PATH            ?= https://helm.lp.rsint.net/helm-dev/rs/coc/platform
HELM_CHART_PACKAGE        := $(CHART_NAME)-$(CHART_VERSION).tgz
HELM_CHART_URL            := $(HELM_REPO_PATH)/$(HELM_CHART_PACKAGE)

all: test

.PHONY: print-app-version
print-app-version:
	@printf '%s\n' '$(APP_VERSION)'

.PHONY: print-chart-version
print-chart-version:
	@printf '%s\n' '$(CHART_VERSION)'

.PHONY: print-chart-name
print-chart-name:
	@printf '%s\n' '$(CHART_NAME)'

.PHONY: print-docker-image
print-docker-image:
	@printf '%s\n' '$(DOCKER_IMAGE)'

.PHONY: print-docker-registry
print-docker-registry:
	@printf '%s\n' '$(DOCKER_REGISTRY)'

.PHONY: print-helm-chart-package
print-helm-chart-package:
	@printf '%s\n' '$(HELM_CHART_PACKAGE)'

.PHONY: print-helm-chart-url
print-helm-chart-url:
	@printf '%s\n' '$(HELM_CHART_URL)'

.PHONY: run
run: fmt vet
	go run $(GO_MOD_FLAGS) main.go

.PHONY: test
test: build crds envtest
	@mkdir -p coverage
	KUBEBUILDER_ASSETS="$$( $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path )" \
	go test $(GO_MOD_FLAGS) -race -p 1 ./... -coverprofile coverage/cover.out

coverage: test
	go tool cover -html=coverage/cover.out -o coverage/coverage.html
	@echo "Coverage report: coverage/coverage.html"

.PHONY: build
build: fmt vet
	rm -f bin/operator
	go build $(GO_MOD_FLAGS) -o bin/operator main.go

.PHONY: fmt
fmt: mod
	go fmt . ./pkg/...

.PHONY: vet
vet:
	go vet $(GO_MOD_FLAGS) ./pkg/...

.PHONY: mod
mod:
	go mod tidy

#
# - - - - - - - - - - Artifact Generation - - - - - - - - - -
#

.PHONY: docker-build
docker-build:
	$(CONTAINER_ENGINE) login ${DOCKER_REGISTRY} --username ${AD_USER} --password ${AD_PASSWORD}
	$(CONTAINER_ENGINE) build -f Dockerfile -t ${DOCKER_IMAGE} .

.PHONY: docker-upload
docker-upload:
	@set -e; \
	$(CONTAINER_ENGINE) login ${DOCKER_REGISTRY} --username ${AD_USER} --password ${AD_PASSWORD}; \
	if $(CONTAINER_ENGINE) manifest inspect ${DOCKER_IMAGE} >/dev/null 2>&1; then \
		echo "Docker image ${DOCKER_IMAGE} already exists in ${DOCKER_REGISTRY}; skipping upload"; \
		exit 0; \
	fi; \
	go test $(GO_MOD_FLAGS) ./...; \
	$(CONTAINER_ENGINE) build -f Dockerfile -t ${DOCKER_IMAGE} .; \
	$(CONTAINER_ENGINE) push ${DOCKER_IMAGE}; \
	$(MAKE) --no-print-directory git-tag-app

.PHONY: helm-package
helm-package:
	helm package helm

.PHONY: helm-upload
helm-upload:
	@set -e; \
	chart="$(HELM_CHART_PACKAGE)"; \
	if [ ! -f "$$chart" ]; then \
		$(MAKE) --no-print-directory helm-package; \
	fi; \
	echo "uploading chart $$chart..."; \
	curl -k -u ${AD_USER}:${AD_PASSWORD} -XPUT "${HELM_REPO_PATH}/$$chart" -T "$$chart"

git-tag:
	chart=$$(ls -t1 pg*.tgz | head -n1 | sed 's/.*-\([0-9]*\.[0-9]*\.[0-9]*\).*$$/\1/'); \
	echo "create git tag $$chart..."; \
	git tag -a $$chart -m "Uses docker version ${DOCKER_TAG}"

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
.PHONY: helm-test
helm-test:
	helm lint helm/

#
# - - - - - - - - - - K8s Testing - - - - - - - - - -
#

.PHONY: k8s-install
k8s-install: docker-upload
	helm install pg-topology helm -n cnpg-system

#
# - - - - - - - - - Git Tagging - - - - - - - - - - -
#

define release_notes
	@awk -v version="$($(1))" '\
		$$0 ~ "^### \\[" version "\\]" { found=1; next } \
		found && (/^## / || /^### /) { exit } \
		found { print } \
	' "$(CHANGELOG)"
endef

define git_tag
	@set -e; \
	tag_prefix="$(1)"; \
	version="$($(2))"; \
	tag="$$tag_prefix/$$version"; \
	if git ls-remote --exit-code --tags "$(GIT_REMOTE)" "refs/tags/$$tag" >/dev/null 2>&1; then \
		echo "Git tag $$tag already exists on $(GIT_REMOTE); skipping Git tag"; \
		exit 0; \
	fi; \
	notes="$$( $(MAKE) --no-print-directory release-notes-$(1) )"; \
	if [ -z "$$notes" ]; then \
		echo "ERROR: No changelog entry found for version $$version" >&2; \
		exit 1; \
	fi; \
	echo "Pushing Git tag $$tag"; \
	if git rev-parse --verify --quiet "refs/tags/$$tag" >/dev/null; then \
		echo "Local Git tag $$tag already exists; pushing it"; \
	else \
		git tag -a "$$tag" --cleanup=verbatim -m "$$notes"; \
	fi; \
	git push "$(GIT_REMOTE)" "$$tag"
endef

.PHONY: release-notes-chart
release-notes-chart:
	$(call release_notes,CHART_VERSION)

.PHONY: release-notes-app
release-notes-app:
	$(call release_notes,APP_VERSION)

.PHONY: git-tag-chart
git-tag-chart:
	$(call git_tag,chart,CHART_VERSION)

.PHONY: git-tag-app
git-tag-app:
	$(call git_tag,app,APP_VERSION)
