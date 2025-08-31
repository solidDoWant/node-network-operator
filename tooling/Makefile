MAKEFLAGS += --no-print-directory
THIS_MAKEFILE_DIRECTORY = $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
ROOT_MAKEFILE_DIRECTORY ?= $(abspath $(THIS_MAKEFILE_DIRECTORY)..)
LOCALBIN = $(shell $(MAKE) -C $(ROOT_MAKEFILE_DIRECTORY) --no-print-directory print-localbin)

include schema.mk

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: build
build: fmt vet

.PHONY: clean
clean:
	rm -rf bin/

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

# Dev tooling
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT):
	@$(MAKE) -C $(ROOT_MAKEFILE_DIRECTORY) golangci-lint
