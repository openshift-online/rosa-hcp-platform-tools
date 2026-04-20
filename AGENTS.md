# AGENTS.md

This file provides guidance to AI coding assistants when working with this repository.

## Project Overview

ROSA HCP Platform Tools — a central repository for tools and automation used by the ROSA HCP Platform team. Contains various Go CLI tools for platform operations.

## Build & Test Commands

```bash
make build-all       # Build all tools
make test-all        # Run all tests
make vet-all         # Run go vet on all tools
make lint-shell-all  # Lint all shell scripts
```

## Architecture

- **tools/**: Individual tools, each in its own subdirectory with independent `go.mod`

## Key Conventions

- Each tool is a standalone Go module under `tools/`
- Tools share no Go dependencies between them
- Build and test targets operate on all tools simultaneously
