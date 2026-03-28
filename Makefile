APP := openkiro
VERSION ?= dev
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint vet check clean snapshot release-dry \
        sandbox-claude sandbox-kiro sandbox-all \
        tag-patch tag-minor tag-major

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(APP) ./cmd/openkiro

# ── Docker Sandbox templates (Docker Desktop 4.58+) ──────────────────────────
# Build the custom sandbox templates that extend the official Docker Sandbox
# base images and add the openkiro proxy + companion tools.
#
# Usage after building:
#   docker sandbox run --template openkiro-sandbox-claude:latest my-session ~/project
#   docker sandbox run --template openkiro-sandbox-kiro:latest   my-session ~/project

sandbox-claude:
	docker build -f Dockerfile.sandbox-claude \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(DATE) \
	  -t openkiro-sandbox-claude:latest .

sandbox-kiro:
	docker build -f Dockerfile.sandbox-kiro \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(DATE) \
	  -t openkiro-sandbox-kiro:latest .

sandbox-all: sandbox-claude sandbox-kiro

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 || { echo "install: brew install golangci-lint"; exit 1; }
	golangci-lint run

check: lint test
	@echo "All quality gates passed"

snapshot:
	goreleaser release --snapshot --clean

release-dry:
	goreleaser release --skip=publish --clean

clean:
	rm -rf bin/ dist/

# ── Semantic version tagging ──

LATEST_TAG := $(shell git tag -l 'v*' --sort=-v:refname | head -1)
LATEST_TAG := $(if $(LATEST_TAG),$(LATEST_TAG),v0.0.0)
MAJOR := $(shell echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f1)
MINOR := $(shell echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f2)
PATCH := $(shell echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f3 | cut -d- -f1)

tag-patch: check
	$(eval NEW := v$(MAJOR).$(MINOR).$(shell echo $$(($(PATCH)+1))))
	@echo "$(LATEST_TAG) → $(NEW)"
	git tag -a $(NEW) -m "Release $(NEW)"
	git push origin $(NEW)

tag-minor: check
	$(eval NEW := v$(MAJOR).$(shell echo $$(($(MINOR)+1))).0)
	@echo "$(LATEST_TAG) → $(NEW)"
	git tag -a $(NEW) -m "Release $(NEW)"
	git push origin $(NEW)

tag-major: check
	$(eval NEW := v$(shell echo $$(($(MAJOR)+1))).0.0)
	@echo "$(LATEST_TAG) → $(NEW)"
	git tag -a $(NEW) -m "Release $(NEW)"
	git push origin $(NEW)
