# Repository Guidelines

## Project Structure & Module Organization
This module is split into three Go packages plus one binary entrypoint. `smpp/` contains the SMPP 3.4 client, pooling, handlers, and protocol tests. `gateway/` contains the embeddable SMSC gateway, REST/admin APIs, routing, metrics, and the static Admin UI assets in `gateway/admin-ui/`. `mocksmsc/` provides a mock SMSC used by tests. `cmd/smsc-gateway/` builds the standalone gateway binary. End-to-end coverage lives in `e2e/`.

## Build, Test, and Development Commands
Use Go 1.26 locally to match `go.mod` and CI.

- `go build ./...` builds all packages.
- `CGO_ENABLED=0 go build -o smsc-gateway ./cmd/smsc-gateway` builds the static gateway binary.
- `go run ./cmd/smsc-gateway` starts the gateway from source.
- `go test ./...` runs the full test suite.
- `go test ./... -race -count=1 -timeout 300s` matches the main CI test job.
- `go test ./e2e/... -v -count=1 -timeout 60s` runs end-to-end integration tests.
- `go vet ./...` and `golangci-lint run ./...` cover static checks used by contributors and CI.

## Coding Style & Naming Conventions
Follow standard Go formatting: run `gofmt` on changed files and keep imports organized by the Go toolchain. Use tabs for indentation as emitted by `gofmt`. Exported identifiers use `CamelCase`; unexported helpers use `camelCase`. Keep package names short and lowercase (`smpp`, `gateway`, `mocksmsc`). Prefer focused files by responsibility, following existing names such as `rest_api.go`, `admin_auth.go`, and `pool_manager.go`.

## Testing Guidelines
Keep tests adjacent to the code they cover in `*_test.go` files. Current naming follows `TestType_Behavior`, for example `TestRESTSubmit_Success` and `TestAdminUser_ValidateJWT`. Add unit tests for new logic and extend `e2e/` when behavior crosses package boundaries. Run race-enabled tests before opening a PR.

## Commit & Pull Request Guidelines
Recent history uses conventional prefixes: `feat:`, `fix:`, and `docs:`. Keep commits focused and atomic. PRs should describe the user-visible change, list validation commands run, and note config or API impacts. Link related issues when applicable, and include screenshots only for Admin UI changes.

## Security & Configuration Tips
Runtime configuration is environment-driven through `GW_*` variables; avoid hardcoding credentials in code or tests. Keep test data disposable and prefer temporary data directories over shared local state.
