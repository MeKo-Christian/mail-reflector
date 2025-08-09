# Mail Reflector - Development Task Runner
# Run `just` to see available commands

# Variables
export GO_VERSION := "1.23.0"
export APP_NAME := "mail-reflector"
export WEB_PORT := "8080"
export WEB_BIND := "127.0.0.1"

# Default recipe - show available commands
default:
    @echo "Mail Reflector - Available Commands:"
    @echo ""
    @echo "🔨 Development:"
    @echo "  build        - Build the application"
    @echo "  clean        - Clean build artifacts"
    @echo "  fmt          - Format code using treefmt"
    @echo "  lint         - Run linting with golangci-lint"
    @echo "  lint-fix     - Run linting and fix auto-fixable issues"
    @echo "  deps         - Download and tidy dependencies"
    @echo ""
    @echo "🧪 Testing:"
    @echo "  test         - Run all tests"
    @echo "  test-verbose - Run tests with verbose output"
    @echo "  test-cover   - Run tests with coverage"
    @echo ""
    @echo "🏃 Running:"
    @echo "  init         - Create config.yaml interactively"
    @echo "  check        - Run one-time mailbox check"
    @echo "  serve        - Start IMAP IDLE monitoring"
    @echo "  web          - Start web interface (default: 127.0.0.1:8080)"
    @echo "  web-dev      - Start web interface with verbose logging"
    @echo ""
    @echo "📋 Operations:"
    @echo "  config       - Show current configuration"
    @echo "  backup       - Create manual config backup"
    @echo "  logs         - Show recent logs (if using systemd)"
    @echo ""
    @echo "📦 Release:"
    @echo "  install      - Install binary to /usr/local/bin"
    @echo "  release      - Build release binaries for multiple platforms"
    @echo ""
    @echo "🧹 Maintenance:"
    @echo "  update-deps  - Update all dependencies"
    @echo "  security     - Run security audit"

# Build the application
build:
    @echo "🔨 Building {{APP_NAME}}..."
    go build -ldflags="-s -w" -o {{APP_NAME}} .
    @echo "✅ Build complete: ./{{APP_NAME}}"

# Clean build artifacts
clean:
    @echo "🧹 Cleaning build artifacts..."
    rm -f {{APP_NAME}} {{APP_NAME}}.exe
    rm -rf dist/ build/
    go clean -cache -testcache -modcache
    @echo "✅ Clean complete"

# Format code
fmt:
    @echo "📝 Formatting code..."
    treefmt --allow-missing-formatter
    @echo "✅ Code formatted"

# Run static analysis with golangci-lint
lint:
    @echo "🔍 Running linting with golangci-lint..."
    @if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run; \
    else \
        echo "📦 golangci-lint not found, installing..."; \
        go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
        golangci-lint run; \
    fi
    @echo "✅ Linting complete"

# Run static analysis and fix auto-fixable issues
lint-fix:
    @echo "🔧 Running linting with auto-fix..."
    @if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run --fix; \
    else \
        echo "📦 golangci-lint not found, installing..."; \
        go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
        golangci-lint run --fix; \
    fi
    @echo "✅ Linting with fixes complete"

# Download and tidy dependencies
deps:
    @echo "📦 Managing dependencies..."
    go mod download
    go mod tidy
    @echo "✅ Dependencies updated"

# Run all tests
test:
    @echo "🧪 Running tests..."
    go test ./...
    @echo "✅ Tests complete"

# Run tests with verbose output
test-verbose:
    @echo "🧪 Running tests (verbose)..."
    go test -v ./...

# Run tests with coverage
test-cover:
    @echo "🧪 Running tests with coverage..."
    go test -cover ./...
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "✅ Coverage report generated: coverage.html"

# Create config.yaml interactively
init: build
    @echo "⚙️  Creating configuration..."
    ./{{APP_NAME}} init

# Run one-time mailbox check
check: build
    @echo "📬 Running mailbox check..."
    ./{{APP_NAME}} check --verbose

# Start IMAP IDLE monitoring
serve: build
    @echo "🏃 Starting IMAP IDLE monitoring..."
    ./{{APP_NAME}} serve --verbose

# Start web interface
web: build
    @echo "🌐 Starting web interface..."
    @echo "📍 URL: http://{{WEB_BIND}}:{{WEB_PORT}}"
    @echo "🔐 Default login: admin / admin123"
    ./{{APP_NAME}} web --port {{WEB_PORT}} --bind {{WEB_BIND}}

# Start web interface with verbose logging
web-dev: build
    @echo "🌐 Starting web interface (development mode)..."
    @echo "📍 URL: http://{{WEB_BIND}}:{{WEB_PORT}}"
    @echo "🔐 Default login: admin / admin123"
    ./{{APP_NAME}} web --port {{WEB_PORT}} --bind {{WEB_BIND}} --verbose

# Start web interface on different port
web-alt port="8090": build
    @echo "🌐 Starting web interface on port {{port}}..."
    @echo "📍 URL: http://{{WEB_BIND}}:{{port}}"
    ./{{APP_NAME}} web --port {{port}} --bind {{WEB_BIND}} --verbose

# Show current configuration
config:
    @echo "⚙️  Current configuration:"
    @if [ -f config.yaml ]; then \
        echo "📁 Config file exists: config.yaml"; \
        echo ""; \
        cat config.yaml; \
    else \
        echo "❌ No config.yaml found. Run 'just init' to create one."; \
    fi

# Create manual config backup
backup:
    @echo "💾 Creating manual config backup..."
    @if [ -f config.yaml ]; then \
        mkdir -p config_backups; \
        cp config.yaml "config_backups/manual_backup_$(date +%Y-%m-%d_%H-%M-%S).yaml"; \
        echo "✅ Backup created in config_backups/"; \
    else \
        echo "❌ No config.yaml found to backup."; \
    fi

# Show recent logs (systemd)
logs:
    @echo "📜 Recent logs (if running as systemd service)..."
    @if command -v journalctl >/dev/null 2>&1; then \
        journalctl -u mail-reflector -f --no-pager -n 50; \
    else \
        echo "❌ journalctl not available. Check logs manually."; \
    fi

# Install binary to system
install: build
    @echo "📦 Installing {{APP_NAME}} to /usr/local/bin..."
    sudo cp {{APP_NAME}} /usr/local/bin/
    sudo chmod +x /usr/local/bin/{{APP_NAME}}
    @echo "✅ Installed successfully"
    @echo "💡 You can now run '{{APP_NAME}}' from anywhere"

# Build release binaries for multiple platforms
release:
    @echo "🚀 Building release binaries..."
    mkdir -p dist
    
    # Linux AMD64
    @echo "Building for Linux AMD64..."
    GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/{{APP_NAME}}-linux-amd64 .
    
    # Linux ARM64
    @echo "Building for Linux ARM64..."
    GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/{{APP_NAME}}-linux-arm64 .
    
    # MacOS AMD64
    @echo "Building for macOS AMD64..."
    GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o dist/{{APP_NAME}}-darwin-amd64 .
    
    # MacOS ARM64 (Apple Silicon)
    @echo "Building for macOS ARM64..."
    GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/{{APP_NAME}}-darwin-arm64 .
    
    # Windows AMD64
    @echo "Building for Windows AMD64..."
    GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/{{APP_NAME}}-windows-amd64.exe .
    
    @echo "✅ Release binaries created in dist/"
    @ls -la dist/

# Update all dependencies
update-deps:
    @echo "⬆️  Updating dependencies..."
    go get -u ./...
    go mod tidy
    @echo "✅ Dependencies updated"

# Run security audit
security:
    @echo "🔒 Running security audit..."
    @if command -v govulncheck >/dev/null 2>&1; then \
        govulncheck ./...; \
    else \
        echo "📦 Installing govulncheck..."; \
        go install golang.org/x/vuln/cmd/govulncheck@latest; \
        govulncheck ./...; \
    fi
    @echo "✅ Security audit complete"

# Development workflow - format, lint-fix, test, build
dev: fmt lint-fix test build
    @echo "🎉 Development workflow complete!"

# Full CI workflow
ci: deps fmt lint test-cover build
    @echo "🎉 CI workflow complete!"

# Quick start for new developers
quickstart:
    @echo "🚀 Quick start for Mail Reflector"
    @echo ""
    @echo "1. Install dependencies:"
    @echo "   just deps"
    @echo ""
    @echo "2. Create configuration:"
    @echo "   just init"
    @echo ""
    @echo "3. Test the setup:"
    @echo "   just check"
    @echo ""
    @echo "4. Start web interface:"
    @echo "   just web-dev"
    @echo ""
    @echo "5. Or start IMAP monitoring:"
    @echo "   just serve"
    @echo ""
    @echo "📚 Run 'just' to see all available commands"

# Docker-related tasks (for future containerization)
docker-build:
    @echo "🐳 Building Docker image..."
    docker build -t {{APP_NAME}}:latest .

docker-run:
    @echo "🐳 Running Docker container..."
    docker run -it --rm -p {{WEB_PORT}}:{{WEB_PORT}} -v $(pwd)/config.yaml:/app/config.yaml {{APP_NAME}}:latest

# Systemd service management (Linux)
systemd-install: install
    @echo "⚙️  Installing systemd service..."
    @echo "📝 Create /etc/systemd/system/mail-reflector.service manually"
    @echo "💡 Then run: sudo systemctl enable mail-reflector && sudo systemctl start mail-reflector"

systemd-status:
    @echo "📊 Mail Reflector service status:"
    systemctl status mail-reflector --no-pager

systemd-restart:
    @echo "🔄 Restarting Mail Reflector service..."
    sudo systemctl restart mail-reflector
