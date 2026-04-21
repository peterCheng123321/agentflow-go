BINARY  = agentflow
APP     = AgentFlow.app
DIST    = dist
ARCH   ?= arm64

.PHONY: build app run clean test

build:
	CGO_ENABLED=1 GOARCH=$(ARCH) GOOS=darwin go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/

app: build
	rm -rf $(DIST)/$(APP)
	mkdir -p $(DIST)/$(APP)/Contents/MacOS
	mkdir -p $(DIST)/$(APP)/Contents/Resources
	cp $(BINARY) $(DIST)/$(APP)/Contents/MacOS/$(BINARY)
	cp packaging/macos/Info.plist $(DIST)/$(APP)/Contents/Info.plist
	@echo ""
	@echo "Built $(DIST)/$(APP)"
	@echo "Run:  open $(DIST)/$(APP)"
	@echo "If Gatekeeper blocks: xattr -cr $(DIST)/$(APP)"

run: build
	./$(BINARY)

test:
	CGO_ENABLED=1 go test ./...

clean:
	rm -rf $(BINARY) $(DIST) agentflow-ui
