# Contributing to go-smsc

Thank you for your interest in contributing to go-smsc!

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone git@github.com:YOUR_USER/go-smsc.git`
3. Create a branch: `git checkout -b my-feature`
4. Make your changes
5. Run tests: `go test ./... -race`
6. Run vet: `go vet ./...`
7. Commit and push
8. Open a pull request

## Development

### Prerequisites

- Go 1.23 or later
- Docker (for integration tests and building container images)

### Building

```bash
go build ./...                                        # build all packages
CGO_ENABLED=0 go build -o smsc-gateway ./cmd/smsc-gateway  # static binary
```

### Testing

```bash
go test ./...              # all tests
go test ./smpp/... -v      # SMPP library tests
go test ./gateway/... -v   # gateway tests
go test ./... -race        # with race detector
```

### Project Structure

```
smpp/       — SMPP 3.4 protocol library (client, pool, PDU codec)
gateway/    — Embeddable SMSC gateway
mocksmsc/   — Mock SMSC server for testing
cmd/        — Standalone binaries
```

## Guidelines

- Write tests for new functionality
- Run `go vet ./...` before submitting
- Keep commits focused and atomic
- Follow existing code style
- Update documentation for user-facing changes

## Reporting Issues

Use [GitHub Issues](https://github.com/IDNTEQ/go-smsc/issues) to report bugs or request features. Include:

- Go version (`go version`)
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs or error messages

## License

By contributing, you agree that your contributions will be licensed under the project's Apache License 2.0.
