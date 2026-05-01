# Go HTTP server embedded in the SwiftUI macOS app (see macos-app).
BINARY        = agentflow-serve
APP           = AgentFlow.app
DIST          = dist
ARCH          ?= arm64
# Swift package path (sibling of agentflow-go by default).
AGENTFLOW_UI  ?= ../AgentFlowUI

.PHONY: build macos-app app run clean test

build:
	GOARCH=$(ARCH) GOOS=darwin go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/

# Full macOS application: Swift shell + embedded agentflow-serve (same layout BackendManager expects).
macos-app: build
	@test -d "$(AGENTFLOW_UI)" || (echo "Set AGENTFLOW_UI=… to your AgentFlowUI package directory"; exit 1)
	cd "$(AGENTFLOW_UI)" && swift build -c release
	SWIFT_BIN=$$(cd "$(AGENTFLOW_UI)" && swift build -c release --show-bin-path)/AgentFlowUI; \
	test -f "$$SWIFT_BIN" || (echo "Swift build did not produce AgentFlowUI"; exit 1); \
	rm -rf "$(DIST)/$(APP)"; \
	mkdir -p "$(DIST)/$(APP)/Contents/MacOS"; \
	cp "$(BINARY)" "$(DIST)/$(APP)/Contents/MacOS/$(BINARY)"; \
	cp "$$SWIFT_BIN" "$(DIST)/$(APP)/Contents/MacOS/AgentFlowUI"; \
	chmod +x "$(DIST)/$(APP)/Contents/MacOS/AgentFlowUI"; \
	cp packaging/macos/Info.plist "$(DIST)/$(APP)/Contents/Info.plist"; \
	codesign -s - --force --deep "$(DIST)/$(APP)" 2>/dev/null || true; \
	echo ""; \
	echo "Built $(DIST)/$(APP)"; \
	echo "Run:  open $(DIST)/$(APP)"; \
	echo "If Gatekeeper blocks: xattr -cr $(DIST)/$(APP)"

app: macos-app

run: build
	./$(BINARY)

test:
	go test ./...

clean:
	rm -rf $(BINARY) $(DIST) agentflow-ui
