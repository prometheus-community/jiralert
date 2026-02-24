DOCKER_REPO             ?= quay.io/jiralert
DOCKER_IMAGE_NAME       ?= jiralert


.PHONY: all # Similar to default command for common, but without yamllint
all: test build

include Makefile.common
