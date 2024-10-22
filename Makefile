DOCKER_REPO             ?= quay.io/jiralert
DOCKER_IMAGE_NAME       ?= jiralert

.PHONY: build
build:
	go build -ldflags "-w -s -X main.Version=github.com/cloudeteer/jiralert@$$(git rev-parse --short HEAD)" -a -tags netgo -o jiralert ./cmd/jiralert

.Phone: docker-build
docker-build:
	docker buildx build --no-cache --push -t ghcr.io/cloudeteer/jiralert:$$(git rev-parse --short HEAD) --platform linux/arm64,linux/amd64 .

include Makefile.common
