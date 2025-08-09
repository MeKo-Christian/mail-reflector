# mail-reflector justfile
# Run with: just <command>

# Show available commands
default:
    @just --list

# Build the application
build:
    @echo "Building mail-reflector..."
    go build -o mail-reflector .

# Run tests
test:
    @echo "Running tests..."
    go test ./...

# Run tests with verbose output
test-verbose:
    @echo "Running tests with verbose output..."
    go test -v ./...

# Run specific package tests
test-reflector:
    @echo "Running reflector package tests..."
    go test -v ./internal/reflector

# Format code using treefmt
format:
    @echo "Formatting code..."
    treefmt --allow-missing-formatter

# Run linter
lint:
    @echo "Running golangci-lint..."
    golangci-lint run

# Run linter with fixes
lint-fix:
    @echo "Running golangci-lint with fixes..."
    golangci-lint run --fix

# Run static analysis
vet:
    @echo "Running go vet..."
    go vet ./...

# Clean up dependencies
tidy:
    @echo "Tidying dependencies..."
    go mod tidy

# Run all checks (format, lint, vet, test)
check: format lint vet test
    @echo "All checks passed!"

# Development workflow (format, lint-fix, test)
dev: format lint-fix test
    @echo "Development checks completed!"

# Full CI workflow
ci: tidy format lint vet test build
    @echo "CI workflow completed!"

# Create a release build
release: clean ci
    @echo "Creating release build..."
    go build -ldflags="-s -w" -o mail-reflector .

# Clean build artifacts
clean:
    @echo "Cleaning build artifacts..."
    @rm -f mail-reflector

# Run the application with check command
run-check: build
    @echo "Running mail-reflector check..."
    ./mail-reflector check --verbose

# Run the application with serve command
run-serve: build
    @echo "Running mail-reflector serve..."
    ./mail-reflector serve --verbose

# Initialize configuration interactively
init-config: build
    @echo "Initializing configuration..."
    ./mail-reflector init

# Show version
version: build
    @echo "Showing version..."
    ./mail-reflector version

# Install dependencies (if needed)
deps:
    @echo "Installing dependencies..."
    go mod download

# Generate documentation (if needed)
docs:
    @echo "Generating documentation..."
    go doc ./...

# Run security scan
security:
    @echo "Running security scan..."
    go list -json -deps ./... | nancy sleuth

# Run benchmarks (if any exist)
bench:
    @echo "Running benchmarks..."
    go test -bench=. ./...