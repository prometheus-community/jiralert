GO := go

VERSION := $(shell git describe --tags 2>/dev/null)
ifeq "$(VERSION)" ""
VERSION := $(shell git rev-parse --short HEAD)
endif

RELEASE     := jiralert-$(VERSION).linux-amd64
RELEASE_DIR := release/$(RELEASE)

PACKAGES           := $(shell $(GO) list ./... | grep -v /vendor/)
STATICCHECK_IGNORE :=

all: clean format staticcheck build

clean:
	@rm -rf jiralert release

format:
	@echo ">> formatting code"
	@$(GO) fmt $(PACKAGES)

staticcheck: get_staticcheck
	@echo ">> running staticcheck"
	@staticcheck -ignore "$(STATICCHECK_IGNORE)" $(PACKAGES)

build:
	@echo ">> building binaries"
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags "-X main.Version=$(VERSION)" github.com/free/jiralert/cmd/jiralert

tarball:
	@echo ">> packaging release $(VERSION)"
	@rm -rf "$(RELEASE_DIR)/*"
	@mkdir -p "$(RELEASE_DIR)"
	@cp jiralert README.md LICENSE "$(RELEASE_DIR)"
	@mkdir -p "$(RELEASE_DIR)/config"
	@cp config/* "$(RELEASE_DIR)/config"
	@tar -zcvf "$(RELEASE).tar.gz" -C "$(RELEASE_DIR)"/.. "$(RELEASE)"
	@rm -rf "$(RELEASE_DIR)"

get_staticcheck:
	@echo ">> getting staticcheck"
	@GOOS= GOARCH= $(GO) get -u honnef.co/go/tools/cmd/staticcheck
