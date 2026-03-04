# Contributing to Aarv

Thank you for your interest in contributing to Aarv! This document provides guidelines and instructions for contributing.

## Code of Conduct

Be respectful and constructive. We're all here to build something useful.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/aarv.git`
3. Create a branch: `git checkout -b feature/your-feature-name`
4. Make your changes
5. Run tests: `go test ./...`
6. Commit: `git commit -am "feat: add your feature"`
7. Push: `git push origin feature/your-feature-name`
8. Open a Pull Request

## Development Setup

```bash
# Clone
git clone https://github.com/nilshah80/aarv.git
cd aarv

# Install dependencies
go mod download

# Run tests
go test ./...

# Run tests with race detector
go test -race ./...

# Run benchmarks
cd bench && go test -bench=. -benchmem

# Run linter (install first: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
golangci-lint run
```

## Pull Request Guidelines

### Before Submitting

- [ ] Run `go test ./...` and ensure all tests pass
- [ ] Run `go test -race ./...` to check for race conditions
- [ ] Run `golangci-lint run` and fix any issues
- [ ] Add tests for new functionality
- [ ] Update documentation if needed

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add new feature
fix: fix bug in X
docs: update README
test: add tests for Y
perf: improve performance of Z
refactor: restructure code
chore: update dependencies
```

### PR Title

Use the same format as commit messages:
- `feat: add WebSocket support`
- `fix: handle nil pointer in Context.JSON`
- `docs: add middleware examples`

## What to Contribute

### Good First Issues

Look for issues labeled `good first issue` - these are beginner-friendly.

### Feature Ideas

- Additional middleware (rate limiting, CORS, JWT auth)
- Database integration examples
- Template rendering support
- WebSocket support
- OpenAPI/Swagger integration

### Documentation

- Improve GoDoc comments
- Add examples to `examples/` folder
- Write tutorials or guides

### Testing

- Increase test coverage
- Add edge case tests
- Add integration tests

## Code Style

- Follow standard Go conventions
- Use `gofmt` for formatting
- Keep functions small and focused
- Add comments for exported types and functions
- Avoid unnecessary dependencies in core

## Questions?

Open an issue with the `question` label or start a discussion.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
