# rosa-hcp-platform-tools

Central repository for storing tools and automation used by the ROSA HCP Platform team.

## Installation

Clone the repository:

```bash
git clone https://github.com/openshift-online/rosa-hcp-platform-tools.git
cd rosa-hcp-platform-tools
```

Each tool under `tools/` is a standalone Go module. Build individual tools:

```bash
cd tools/<tool-name>
go build .
```

Or build all tools at once:

```bash
make build-all
```

## Usage

Each tool has its own usage. Navigate to the specific tool directory and run:

```bash
cd tools/<tool-name>
./<tool-name> --help
```

See individual tool README files under `tools/` for detailed usage.

## Development

### Building and Testing

```bash
make build-all        # Build all tools
make test-all         # Test all tools
make vet-all          # Run go vet on all tools
make lint-shell-all   # Lint all shell scripts
```

### Adding a New Tool

1. Create a new directory under `tools/`
2. Initialize a Go module: `go mod init`
3. Implement the tool
4. Update the Makefile targets if needed
5. Submit a pull request
