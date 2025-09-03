MAKEFLAGS += --no-print-directory
PROJECT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
MODULE_NAME := $(shell go list -m)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: schemas
schemas: manifests
	$(MAKE) -C ./tooling schemas

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: check-licenses
check-licenses: ## Check licenses of dependencies.
	@go run github.com/google/go-licenses@latest report ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest $(GINKGO) ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" $(GINKGO) run -race -cover -vet="" $$(go list ./... | grep -v /e2e | sed "s~$$(go list -m)/~~")

.PHONY: run
run: manifests generate fmt vet schemas ## Run a controller from your host.
	go run ./cmd/main.go

KIND_CLUSTER ?= node-network-operator-test-e2e

.PHONY: print-kind-cluster-name
print-kind-cluster-name:
	@echo $(KIND_CLUSTER)

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	$(KIND) create cluster --name $(KIND_CLUSTER) --config ./test/kind-cluster.yaml

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND_CLUSTER=$(KIND_CLUSTER) go test ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

##@ Build and Release

VERSION = 0.0.1-dev
CONTAINER_REGISTRY = ghcr.io/soliddowant
HELM_REGISTRY = ghcr.io/soliddowant/charts
PUSH_ALL ?= false

BUILD_DIR := $(PROJECT_DIR)/build

BINARY_DIR = $(BUILD_DIR)/binaries
BINARY_PLATFORMS = linux/amd64 linux/arm64
BINARY_NAME = node-network-operator
GO_SOURCE_FILES = $(shell find ./cmd ./internal ./api \( -name '*.go' ! -name '*_test.go' \))
GO_LDFLAGS := -s -w

LOCALOS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
LOCALARCH := $(shell uname -m | sed 's/x86_64/amd64/')
LOCAL_BINARY_PATH := $(BINARY_DIR)/$(LOCALOS)/$(LOCALARCH)/$(BINARY_NAME)

$(BINARY_DIR)/%/$(BINARY_NAME): $(GO_SOURCE_FILES)
	@mkdir -p "$(@D)"
	@CGO_ENABLED=0 GOOS="$(word 1,$(subst /, ,$*))" GOARCH="$(word 2,$(subst /, ,$*))" go build -ldflags="$(GO_LDFLAGS)" -o "$@" ./cmd/

LOCAL_BUILDERS += binary
.PHONY: binary
binary: $(LOCAL_BINARY_PATH)	## Build the operator binary for the local platform.

ALL_BUILDERS += binary-all
.PHONY: binary-all
binary-all: $(BINARY_PLATFORMS:%=$(BINARY_DIR)/%/$(BINARY_NAME))	## Build the operator binary for all supported platforms.

LICENSE_DIR = $(BUILD_DIR)/licenses
GO_DEPENDENCIES_LICENSE_DIR = $(LICENSE_DIR)/go-dependencies
BUILT_LICENSES := $(LICENSE_DIR)/LICENSE $(GO_DEPENDENCIES_LICENSE_DIR)

$(BUILT_LICENSES) &: go.mod LICENSE
	@mkdir -p "$(LICENSE_DIR)"
	@cp LICENSE "$(LICENSE_DIR)"
	@rm -rf "$(GO_DEPENDENCIES_LICENSE_DIR)"
	@go run github.com/google/go-licenses@latest save ./... --save_path="$(GO_DEPENDENCIES_LICENSE_DIR)" --ignore "$(MODULE_NAME)"

ALL_BUILDERS += licenses
.PHONY: licenses
licenses: $(BUILT_LICENSES)	## Gather licenses of the project and its dependencies.

TARBALL_DIR = $(BUILD_DIR)/tarballs
LOCAL_TARBALL_PATH := $(TARBALL_DIR)/$(LOCALOS)/$(LOCALARCH)/$(BINARY_NAME).tar.gz

$(TARBALL_DIR)/%/$(BINARY_NAME).tar.gz: $(BINARY_DIR)/%/$(BINARY_NAME) licenses
	@mkdir -p "$(@D)"
	@tar -czf "$@" -C "$(BINARY_DIR)/$*" "$(BINARY_NAME)" -C "$(dir $(LICENSE_DIR))" "$(notdir $(LICENSE_DIR))"

PHONY += tarball
LOCAL_BUILDERS += tarball
tarball: $(LOCAL_TARBALL_PATH)	## Create a tarball with the operator binary and licenses for the local platform.

PHONY += tarball-all
ALL_BUILDERS += tarball-all
tarball-all: $(BINARY_PLATFORMS:%=$(TARBALL_DIR)/%/$(BINARY_NAME).tar.gz)	## Create tarballs with the operator binary and licenses for all supported platforms.

CONTAINER_IMAGE_TAG = $(CONTAINER_REGISTRY)/$(BINARY_NAME):$(VERSION)
CONTAINER_BUILD_LABEL_VARS = org.opencontainers.image.source=https://github.com/solidDoWant/node-network-operator org.opencontainers.image.licenses=AGPL-3.0
CONTAINER_BUILD_LABELS := $(foreach var,$(CONTAINER_BUILD_LABEL_VARS),--label $(var))
CONTAINER_PLATFORMS := $(BINARY_PLATFORMS)

.PHONY: print-container-image-tag
print-container-image-tag:	## Print the container image tag.
	@echo $(CONTAINER_IMAGE_TAG)

LOCAL_BUILDERS += container-image
.PHONY: container-image
container-image: binary licenses	## Build the container image for the local platform.
	$(CONTAINER_TOOL) buildx build --platform linux/$(LOCALARCH) -t $(CONTAINER_IMAGE_TAG) --load $(CONTAINER_BUILD_LABELS) .

CONTAINER_MANIFEST_PUSH ?= $(PUSH_ALL)

ALL_BUILDERS += container-manifest
.PHONY: container-manifest
container-manifest: PUSH_ARG = $(if $(findstring t,$(CONTAINER_MANIFEST_PUSH)),--push)
container-manifest: $(CONTAINER_PLATFORMS:%=$(BINARY_DIR)/%/$(BINARY_NAME)) licenses	## Build and optionally push the container image for all supported platforms.
	@docker buildx build $(CONTAINER_PLATFORMS:%=--platform %) $(PUSH_ARG) -t $(CONTAINER_IMAGE_TAG) $(CONTAINER_BUILD_LABELS) .

HELM_CHART_DIR := $(PROJECT_DIR)/deploy/charts/node-network-operator
HELM_CHART_FILES = $(shell find $(HELM_CHART_DIR) -type f)
HELM_PACKAGE = $(BUILD_DIR)/helm/node-network-operator-$(VERSION).tgz
HELM_PUSH ?= $(PUSH_ALL)

$(HELM_PACKAGE): PUSH_CHECK = $(if $(findstring t,$(HELM_PUSH)),true,false)
$(HELM_PACKAGE): $(HELM_CHART_FILES)
	@mkdir -p "$(@D)"
	@helm package "$(HELM_CHART_DIR)" --dependency-update --version "$(VERSION)" --app-version "$(VERSION)" --destination "$(@D)"
	@$(PUSH_CHECK) && helm push "$(HELM_PACKAGE)" oci://$(HELM_REGISTRY) || true

LOCAL_BUILDERS += helm
ALL_BUILDERS += helm
.PHONY: helm
helm: $(HELM_PACKAGE)	## Package and optionally push the Helm chart to the registry.

.PHONY: print-helm-package
print-helm-package:	## Print the Helm package path.
	@echo $(HELM_PACKAGE)

.PHONY: build-installer
LOCAL_BUILDERS += build-installer
build-installer: INSTALLER_DIR := $(BUILD_DIR)/installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${CONTAINER_IMAGE_TAG}
	mkdir -p "$(INSTALLER_DIR)"
	$(KUSTOMIZE) build config/default > "$(INSTALLER_DIR)/install.yaml"

.PHONY: build
build: manifests generate fmt vet schemas $(LOCAL_BUILDERS) ## Builds all local outputs (binaries, tarballs, licenses, etc.).

.PHONY: build-all
build-all: $(ALL_BUILDERS)	## Builds all outputs for all supported platforms (binaries, tarballs, licenses, etc.).

.PHONY: clean
clean:	## Clean up all build artifacts.
	@rm -rf $(BUILD_DIR) $(WORKING_DIR) $(HELM_CHART_DIR)/charts
	@docker image rm -f $(CONTAINER_IMAGE_TAG) 2> /dev/null > /dev/null || true

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests schemas kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests schemas kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${CONTAINER_IMAGE_TAG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

TOOLING_DIR ?= $(dir $(abspath $(lastword $(MAKEFILE_LIST))))tooling

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

print-localbin:
	@echo $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
GINKGO = $(LOCALBIN)/ginkgo

## Tool Versions
KUSTOMIZE_VERSION ?= v5.6.0
CONTROLLER_TOOLS_VERSION ?= v0.18.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.1.0
GINKGO_VERSION ?= $(shell go list -m -f "{{ .Version }}" github.com/onsi/ginkgo/v2)

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: ginkgo
ginkgo: $(GINKGO) ## Download ginkgo locally if necessary.
$(GINKGO): $(LOCALBIN)
	$(call go-install-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo,$(GINKGO_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef
