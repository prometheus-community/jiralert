GO          := go
GOIMPORTS   ?= goimports
STATICCHECK ?= staticcheck

VERSION := $(shell git describe --tags 2>/dev/null)
ifeq "$(VERSION)" ""
VERSION := $(shell git rev-parse --short HEAD)
endif

RELEASE     := jiralert-$(VERSION).linux-amd64
RELEASE_DIR := release/$(RELEASE)

PACKAGES           := $(shell $(GO) list ./... | grep -v /vendor/)
FILES              := $(shell find . -type f -name '*.go' -not -path "./vendor/*")

STATICCHECK_IGNORE :=

DOCKER_IMAGE_NAME := jiralert

all: clean format staticcheck build

clean:
	@rm -rf jiralert release

format:
	@echo ">> formatting code"
	@$(GOIMPORTS) -w $(FILES)

staticcheck: $(STATICCHECK)
	@echo ">> running staticcheck"
	@staticcheck -ignore "$(STATICCHECK_IGNORE)" $(PACKAGES)

build:
	@echo ">> building binaries"
	@# CGO must be disabled to run in busybox container.
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "-X main.Version=$(VERSION)" github.com/free/jiralert/cmd/jiralert

# docker builds docker with no tag.
docker: build
	@echo ">> building docker image '${DOCKER_IMAGE_NAME}'"
	@docker build -t "${DOCKER_IMAGE_NAME}" .

tarball:
	@echo ">> packaging release $(VERSION)"
	@rm -rf "$(RELEASE_DIR)/*"
	@mkdir -p "$(RELEASE_DIR)"
	@cp jiralert README.md LICENSE "$(RELEASE_DIR)"
	@mkdir -p "$(RELEASE_DIR)/config"
	@cp config/* "$(RELEASE_DIR)/config"
	@tar -zcvf "$(RELEASE).tar.gz" -C "$(RELEASE_DIR)"/.. "$(RELEASE)"
	@rm -rf "$(RELEASE_DIR)"

$(STATICCHECK):
	@echo ">> getting staticcheck"
	@GOOS= GOARCH= $(GO) get -u honnef.co/go/tools/cmd/staticcheck
