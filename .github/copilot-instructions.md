# MovieNight Stream Server

MovieNight is a Go-based streaming server with chat functionality, designed to replace Rabbit for watching movies with groups online. It provides HTTP/RTMP servers for streaming, websocket-based chat, and a web interface.

Always reference these instructions first and fallback to search or bash commands only when you encounter unexpected information that does not match the info here.

## Working Effectively

### Bootstrap and Build
- Install Go 1.21 or newer (current go.mod requires 1.21+ with toolchain go1.24.7)
- Clone repository: `git clone https://github.com/zorchenhimer/MovieNight && cd MovieNight`
- Build: `go build` -- takes 23 seconds on first build with dependency download, then <1 second on subsequent builds. NEVER CANCEL. Set timeout to 60+ seconds for first build.
- Alternative build: `make all` -- includes formatting, vetting, testing, and building. Takes ~1 second after initial build.
- Generate settings: `cp settings_example.json settings.json`

### Development Workflow Commands
- Format code: `gofmt -w .` or `make fmt` -- takes <0.1 seconds  
- Vet code: `go vet ./...` or `make vet` -- takes ~0.2 seconds
- Run tests: `go test ./...` or `make test` -- takes <0.2 seconds (cached), ~3 seconds fresh. NEVER CANCEL.
- Run UI tests: `go test ./... -tags=test_ui` -- takes 25+ seconds, requires Playwright dependencies. NEVER CANCEL. Set timeout to 45+ minutes.
- Run linter: `golangci-lint run` -- takes ~5 seconds, shows some known minor issues (safe to ignore)

### Run the Application
- ALWAYS run the bootstrapping steps first (build + settings file)
- Start server: `./MovieNight -f settings.json`
- Server starts in <2 seconds and listens on:
  - HTTP: port 8089 (web interface)
  - RTMP: port 1935 (streaming input)
- Access points:
  - Main: http://localhost:8089/
  - Chat only: http://localhost:8089/chat  
  - Video only: http://localhost:8089/video
- Admin password is auto-generated and displayed in startup logs

### Docker (KNOWN ISSUE)
- Docker build currently FAILS due to Go version mismatch
- Dockerfile uses golang:1.18 but code requires Go 1.21+
- docker-compose will also fail due to this build issue
- To fix: update Dockerfile FROM line to use golang:1.21 or newer

## Validation

### Manual Testing Scenarios
ALWAYS test these scenarios after making changes to ensure functionality:
1. **Basic Server Start**: `./MovieNight -f settings.json` - should start without errors and show listening ports
2. **Web Interface**: Access http://localhost:8089/ - should load main page (200 status)
3. **Chat Interface**: Access http://localhost:8089/chat - should load chat interface (200 status)
4. **Static Assets**: Verify CSS/JS files load from /static/ paths
5. **Command Line Help**: `./MovieNight --help` - should show all CLI options
6. **Settings File**: Ensure settings.json exists before starting server

### Build and Test Validation
- ALWAYS run `make fmt && make vet && make test` before committing changes
- Standard test suite runs in <3 seconds
- UI tests require `sudo npx playwright install-deps` but may fail due to network restrictions
- Use timeouts of 60+ minutes for UI tests if running them
- golangci-lint will show some known issues (deprecated rand.Seed, unchecked errors) - these are pre-existing

### NEVER CANCEL Commands and Timeouts
- Initial Go build: 23 seconds (set 60+ second timeout for safety)
- Subsequent builds: <1 second (cached dependencies)  
- UI tests: 25+ seconds (set 45+ minute timeout due to Playwright setup)
- Standard tests: <3 seconds (use 30+ second timeout)
- Any long-running operation should be given adequate time

## Codebase Navigation

### Project Structure
```
/
├── main.go              # Main entry point and CLI argument handling
├── settings.go          # Configuration management  
├── chatclient.go        # WebSocket chat client handling
├── chatroom.go          # Chat room management
├── handlers.go          # HTTP/RTMP request handlers
├── connection.go        # WebSocket connection management
├── emotes.go           # Emote system
├── Makefile            # Build automation
├── settings_example.json # Default configuration template
├── static/             # Web assets (CSS, JS, HTML templates)
├── common/             # Shared utilities and logging
│   ├── logging.go      # Logging system
│   ├── colors.go       # User color management
│   ├── emotes.go       # Emote processing
│   └── templates.go    # HTML template utilities
├── files/              # File handling utilities
└── .github/workflows/  # CI/CD pipeline
```

### Key Files for Development
- `main.go`: Server initialization, CLI handling, signal management
- `handlers.go`: HTTP endpoints, RTMP handling, websocket upgrades
- `chatroom.go`: Core chat functionality, user management
- `settings.go`: Configuration structure and loading
- `static/js/chat.js`: Client-side chat functionality
- `ui_test.go`: Playwright-based integration tests
- `Makefile`: Build targets and development commands

### Important Constants and Configuration
- Default HTTP port: 8089
- Default RTMP port: 1935  
- Settings file: settings.json (copy from settings_example.json)
- Log levels: debug, info, warn, error
- Default stream key: "ALongStreamKey" (should be changed in production)

### Common Development Tasks
- Adding new chat commands: Edit `common/chatcommands.go`
- Modifying web interface: Edit files in `static/`
- Changing server behavior: Edit `handlers.go` or `main.go`
- Adding configuration options: Edit `settings.go` and `settings_example.json`
- Testing changes: Always run the manual validation scenarios above

### Dependencies and Modules
The project uses Go modules with these key dependencies:
- gorilla/websocket: WebSocket handling
- gorilla/sessions: Session management  
- nareix/joy4: RTMP/streaming functionality
- playwright-go: UI testing (dev dependency)
- alexflint/go-arg: CLI argument parsing

### Known Issues and Workarounds
- Docker build fails due to Go version mismatch in Dockerfile
- UI tests may fail due to Playwright dependency installation issues
- golangci-lint reports some pre-existing minor issues that are non-blocking
- rand.Seed deprecation warning in common/colors.go (cosmetic issue)

### CI/CD Pipeline
- GitHub Actions workflow in `.github/workflows/main.yaml`
- Runs on Go 1.20 in CI (works despite go.mod specifying 1.21+)
- Builds for multiple OS/architecture combinations
- Includes both standard and UI test execution
- Creates Docker images and artifacts

Always follow the build and validation steps above before making any code changes. The application should build quickly and start immediately when properly configured.