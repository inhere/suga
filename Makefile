## suga — Makefile

APP     := suga
MAIN_DIR := ./cmd/suga
GOEXE = $(shell go env GOEXE)
GOPATH = $(shell go env GOPATH)
BINARY  := $(APP)$(GOEXE)

# Build metadata
BUILD_TIME := $(shell date +%Y-%m-%dT%H:%M:%S)
GIT_HASH  := $(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "unknown")
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev-$(GIT_HASH)")

LDFLAGS := -s -w \
	-X main.Version=$(VERSION) \
	-X main.GitCommit=$(GIT_HASH) \
	-X 'main.BuildDate=$(BUILD_TIME)'

.PHONY: all build install run clean clean-dist help latest build-all dump-info latest-yaml build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows smoke-chat smoke-go

DIST_DIR := dist
# 注：值会经 `echo "description: $(DESCRIPTION)"` 写入 latest.yaml，避免 `;`/`:`/引号等
# shell/YAML 元字符（否则 recipe 展开后会被截断成多条命令）。
DESCRIPTION := Suga is a personal coding agent written in Go.

## all: build (default)
all: build

## build: build Go binary (current platform)
build:
	@mkdir -p $(DIST_DIR)
	@echo "🐹 Building Go binary ($(VERSION) @ $(GIT_HASH))..."
	go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY) $(MAIN_DIR)
	@echo "📦 Compressing binary..."
	@if command -v upx >/dev/null 2>&1; then upx -6 --no-progress $(DIST_DIR)/$(BINARY); else echo "⏭ upx not found; skipping compression"; fi
	@echo "✅ Binary: $(DIST_DIR)/$(BINARY) ($$(du -sh $(DIST_DIR)/$(BINARY) | cut -f1))"

## install: install Go binary to $GOPATH/bin
install: build
	@mkdir -p $(GOPATH)/bin
	@# Windows cannot overwrite a running exe (an open chat) but can rename it:
	@# move it aside first; the .old copy is removed once nothing runs it.
	@# Paths are quoted: a Windows GOPATH (D:\env\gopath) loses its backslashes unquoted.
	@rm -f "$(GOPATH)/bin/$(BINARY).old" 2>/dev/null || true
	@if [ -f "$(GOPATH)/bin/$(BINARY)" ]; then mv -f "$(GOPATH)/bin/$(BINARY)" "$(GOPATH)/bin/$(BINARY).old"; fi
	@cp "$(DIST_DIR)/$(BINARY)" "$(GOPATH)/bin/$(BINARY)"
	@echo "✅ Installed to $(GOPATH)/bin/$(BINARY)"

## run: build and run with current directory
run: build
	./$(DIST_DIR)/$(BINARY)

# ─── Cross Compilation ────────────────────────────────────────────────────────

## build-all: cross-compile for all platforms
build-all: clean-dist dump-info build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows latest-yaml
	ls -lh $(DIST_DIR)

## dump-info: dump build info
dump-info:
	@echo "Build Info:"
	@echo "  VERSION: $(VERSION)"
	@echo "  GIT_HASH: $(GIT_HASH)"
	@echo "  BUILD_TIME: $(BUILD_TIME)"

## latest-yaml: generate latest.yaml release metadata
latest-yaml:
	@mkdir -p $(DIST_DIR)
	@{ \
		echo "name: $(APP)"; \
		echo "version: $(VERSION)"; \
		echo "released_at: $(BUILD_TIME)"; \
		echo "description: $(DESCRIPTION)"; \
	} > $(DIST_DIR)/latest.yaml
	@echo "   → $(DIST_DIR)/latest.yaml"

## build-linux: compile for Linux amd64
build-linux:
	@echo "🐧 linux/amd64..."
	@mkdir -p $(DIST_DIR)
	@GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP)-linux-amd64 $(MAIN_DIR)
	upx -6 --no-progress $(DIST_DIR)/$(APP)-linux-amd64
	chmod +x $(DIST_DIR)/$(APP)-linux-amd64
	@echo "   → $(DIST_DIR)/$(APP)-linux-amd64"

## build-linux-arm64: compile for Linux arm64
build-linux-arm64:
	@echo "🐧 linux/arm64..."
	@mkdir -p $(DIST_DIR)
	@GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP)-linux-arm64 $(MAIN_DIR)
	upx -6 --no-progress $(DIST_DIR)/$(APP)-linux-arm64
	chmod +x $(DIST_DIR)/$(APP)-linux-arm64
	@echo "   → $(DIST_DIR)/$(APP)-linux-arm64"

## build-darwin: compile for macOS amd64
build-darwin:
	@echo "🍎 darwin/amd64..."
	@mkdir -p $(DIST_DIR)
	@GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP)-darwin-amd64 $(MAIN_DIR)
	@echo "   → $(DIST_DIR)/$(APP)-darwin-amd64"

## build-darwin-arm64: compile for macOS Apple Silicon
build-darwin-arm64:
	@echo "🍎 darwin/arm64..."
	@mkdir -p $(DIST_DIR)
	@GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP)-darwin-arm64 $(MAIN_DIR)
	# upx -6 --no-progress $(DIST_DIR)/$(APP)-darwin-arm64 # 压缩有问题在 macos 12+
	@echo "   → $(DIST_DIR)/$(APP)-darwin-arm64"

## build-windows: compile for Windows amd64
build-windows:
	@echo "🪟 windows/amd64..."
	@mkdir -p $(DIST_DIR)
	@GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP)-windows-amd64.exe $(MAIN_DIR)
	upx -6 --no-progress $(DIST_DIR)/$(APP)-windows-amd64.exe
	@echo "   → $(DIST_DIR)/$(APP)-windows-amd64.exe"

.PHONY: release
release: build-all ## Create release archives for all platforms TODO 还未启用的
	@echo "Creating release archives..."
	@mkdir -p release
	@cd $(DIST_DIR) && \
	tar -czf ../release/$(APP)-linux-amd64.tar.gz $(APP)-linux-amd64; \
	tar -czf ../release/$(APP)-linux-arm64.tar.gz $(APP)-linux-arm64; \
	tar -czf ../release/$(APP)-darwin-amd64.tar.gz $(APP)-darwin-amd64; \
	tar -czf ../release/$(APP)-darwin-arm64.tar.gz $(APP)-darwin-arm64; \
	zip ../release/$(APP)-windows-amd64.zip $(APP)-windows-amd64.exe;
	@echo "Release archives created in release/"

## smoke-chat: build to tmp/smoke and run the PTY end-to-end chat smoke test (Linux/macOS)
## chat_tool_ctrlc covers Ctrl-C during a foreground shell_exec; the Windows
## twin is scripts/wincons (a Windows console cannot be driven from Python).
smoke-chat:
	@mkdir -p tmp/smoke
	go build -o tmp/smoke/suga ./cmd/suga
	python3 -I scripts/ptysmoke/chat_approval.py --bin tmp/smoke/suga --work tmp/smoke
	python3 -I scripts/ptysmoke/chat_rules.py --bin tmp/smoke/suga --work tmp/smoke-rules
	python3 -I scripts/ptysmoke/chat_edit.py --bin tmp/smoke/suga --work tmp/smoke-edit
	python3 -I scripts/ptysmoke/chat_undo.py --bin tmp/smoke/suga --work tmp/smoke-undo
	python3 -I scripts/ptysmoke/chat_diff.py --bin tmp/smoke/suga --work tmp/smoke-diff
	python3 -I scripts/ptysmoke/chat_memory.py --bin tmp/smoke/suga --work tmp/smoke-memory
	python3 -I scripts/ptysmoke/chat_idle.py --bin tmp/smoke/suga --work tmp/smoke-idle
	python3 -I scripts/ptysmoke/chat_observe.py --bin tmp/smoke/suga --work tmp/smoke-observe
	python3 -I scripts/ptysmoke/chat_retry.py --bin tmp/smoke/suga --work tmp/smoke-retry
	python3 -I scripts/ptysmoke/chat_interrupt.py --bin tmp/smoke/suga --work tmp/smoke-interrupt
	python3 -I scripts/ptysmoke/chat_danger.py --bin tmp/smoke/suga --work tmp/smoke-danger
	python3 -I scripts/ptysmoke/chat_tasks.py --bin tmp/smoke/suga --work tmp/smoke-tasks
	python3 -I scripts/ptysmoke/chat_images.py --bin tmp/smoke/suga --work tmp/smoke-images
	python3 -I scripts/ptysmoke/chat_send_image.py --bin tmp/smoke/suga --work tmp/smoke-send-image
	python3 -I scripts/ptysmoke/chat_commands.py --bin tmp/smoke/suga --work tmp/smoke-commands
	python3 -I scripts/ptysmoke/chat_onboarding.py --bin tmp/smoke/suga --work tmp/smoke-onboarding
	python3 -I scripts/ptysmoke/chat_model.py --bin tmp/smoke/suga --work tmp/smoke-model
	python3 -I scripts/ptysmoke/chat_watch.py --bin tmp/smoke/suga --work tmp/smoke-watch
	python3 -I scripts/ptysmoke/chat_agent.py --bin tmp/smoke/suga --work tmp/smoke-agent
	python3 -I scripts/ptysmoke/chat_spinner.py --bin tmp/smoke/suga --work tmp/smoke-spinner
	python3 -I scripts/ptysmoke/chat_resume.py --bin tmp/smoke/suga --work tmp/smoke-resume
	python3 -I scripts/ptysmoke/chat_resume_cwd.py --bin tmp/smoke/suga --work tmp/smoke-resume-cwd
	python3 -I scripts/ptysmoke/chat_tool_ctrlc.py --bin tmp/smoke/suga --work tmp/smoke-tool-ctrlc
	python3 -I scripts/ptysmoke/chat_steer.py --bin tmp/smoke/suga --work tmp/smoke-steer
	python3 -I scripts/ptysmoke/chat_shortname.py --bin tmp/smoke/suga --work tmp/smoke-shortname
	python3 -I scripts/ptysmoke/chat_adddir.py --bin tmp/smoke/suga --work tmp/smoke-adddir
	python3 -I scripts/ptysmoke/chat_adddir_cmds.py --bin tmp/smoke/suga --work tmp/smoke-adddir-cmds
	python3 -I scripts/ptysmoke/chat_bang.py --bin tmp/smoke/suga --work tmp/smoke-bang
	python3 -I scripts/ptysmoke/chat_paste.py --bin tmp/smoke/suga --work tmp/smoke-paste
	python3 -I scripts/ptysmoke/chat_history.py --bin tmp/smoke/suga --work tmp/smoke-history
	python3 -I scripts/ptysmoke/chat_sanitize.py --bin tmp/smoke/suga --work tmp/smoke-sanitize
	python3 -I scripts/ptysmoke/chat_budget.py --bin tmp/smoke/suga --work tmp/smoke-budget
	python3 -I scripts/ptysmoke/chat_empty_final.py --bin tmp/smoke/suga --work tmp/smoke-empty-final
	python3 -I scripts/ptysmoke/chat_refs.py --bin tmp/smoke/suga --work tmp/smoke-refs
	python3 -I scripts/ptysmoke/preferred_tools.py --bin tmp/smoke/suga --work tmp/smoke-preferred-tools

## smoke-go: run the Go ptytest chat smoke (Linux/macOS and Windows ConPTY)
## Windows: go test -tags ptye2e ./test/ptye2e/... -v
smoke-go:
	go test -tags ptye2e ./test/ptye2e/... -v

## clean: remove build artifacts
clean:
	@rm -f $(BINARY)
	@rm -rf $(DIST_DIR)
	@echo "🧹 Cleaned"

## clean-dist: remove old dist files
clean-dist:
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	@echo "🧹 Cleaned $(DIST_DIR)"

## help: show this help
help:
	@echo "suga Build System"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
