BINARY_NAME=elencode

# The tag is the source of truth for the version; without one this falls back to
# the short commit. See docs/development.md.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# Pinned so a local run matches CI. CI installs these itself, see
# .github/workflows/ci.yml.
GOLANGCI_LINT_VERSION=v2.12.2
GOVULNCHECK_VERSION=v1.6.0
TOOLS_DIR=$(CURDIR)/bin

# golangci-lint pins an older toolchain, and it refuses to lint a module whose
# language version is newer than the one it was built with. Forcing our own
# toolchain builds it against the version go.mod asks for.
TOOLCHAIN=$(shell go env GOVERSION)

.PHONY: build
build:
	go build $(LDFLAGS) -o ./bin/$(BINARY_NAME) ./cmd/elencode

.PHONY: run
run: build
	./bin/$(BINARY_NAME)

.PHONY: test
test:
	go vet ./...
	go test -race ./...

$(TOOLS_DIR)/golangci-lint:
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(TOOLS_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(TOOLS_DIR)/govulncheck:
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(TOOLS_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

.PHONY: lint
lint: $(TOOLS_DIR)/golangci-lint
	$(TOOLS_DIR)/golangci-lint run

.PHONY: fmt
fmt: $(TOOLS_DIR)/golangci-lint
	$(TOOLS_DIR)/golangci-lint fmt

.PHONY: vuln
vuln: $(TOOLS_DIR)/govulncheck
	$(TOOLS_DIR)/govulncheck ./...
