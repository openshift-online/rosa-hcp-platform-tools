.PHONY: build-all test-all vet-all lint-shell-all

GO_TOOLS := $(dir $(wildcard tools/*/go.mod))
SHELL_SCRIPTS := $(wildcard tools/*/*.sh)

## build-all: Build all Go tools
build-all:
	@for tool in $(GO_TOOLS); do \
		echo "Building $$tool..." && \
		(cd $$tool && go build ./...) || exit 1; \
	done

## test-all: Run tests for all Go tools
test-all:
	@for tool in $(GO_TOOLS); do \
		echo "Testing $$tool..." && \
		(cd $$tool && go test -v ./...) || exit 1; \
	done

## vet-all: Run go vet on all Go tools
vet-all:
	@for tool in $(GO_TOOLS); do \
		echo "Vetting $$tool..." && \
		(cd $$tool && go vet ./...) || exit 1; \
	done

## lint-shell-all: Run shellcheck on all shell scripts
lint-shell-all:
	@for script in $(SHELL_SCRIPTS); do \
		echo "Shellchecking $$script..." && \
		shellcheck $$script || exit 1; \
	done
