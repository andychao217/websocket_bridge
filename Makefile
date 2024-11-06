# Copyright (c) Abstract Machines
# SPDX-License-Identifier: Apache-2.0

MG_DOCKER_IMAGE_ALIYUN_PREFIX ?= registry.cn-hangzhou.aliyuncs.com
MG_DOCKER_IMAGE_USERNAME_PREFIX ?= andychao217
MG_DOCKER_IMAGE_NAME_PREFIX ?= magistrala
SVC = socket_bridge
BUILD_DIR = build
CGO_ENABLED ?= 0
GOOS ?= linux
GOARCH ?= arm64

define compile_service
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) \
	go build -o ${BUILD_DIR}/$(SVC)
endef

define make_docker
	docker buildx build --platform=linux/amd64,linux/arm64 \
		--no-cache \
		--build-arg SVC=$(SVC) \
		--tag=$(MG_DOCKER_IMAGE_USERNAME_PREFIX)/$(MG_DOCKER_IMAGE_NAME_PREFIX)-$(SVC) \
		--tag=$(MG_DOCKER_IMAGE_ALIYUN_PREFIX)/$(MG_DOCKER_IMAGE_USERNAME_PREFIX)/$(MG_DOCKER_IMAGE_NAME_PREFIX)-$(SVC) \
		-f docker/Dockerfile .
endef

all: socket_bridge

.PHONY: socket_bridge docker docker_dev run_docker run

install:
	cp ${BUILD_DIR}/$(SVC) $(GOBIN)/magistrala-${SVC}

$(SVC):
	$(call compile_service)

docker:
	$(call make_docker)

docker_dev:
	$(call make_docker_dev)

define docker_push
	docker push ${MG_DOCKER_IMAGE_ALIYUN_PREFIX}/$(MG_DOCKER_IMAGE_USERNAME_PREFIX)/$(MG_DOCKER_IMAGE_NAME_PREFIX)-$(SVC):$(1)
	docker push $(MG_DOCKER_IMAGE_USERNAME_PREFIX)/$(MG_DOCKER_IMAGE_NAME_PREFIX)-$(SVC):$(1)
endef

latest: docker
	$(call docker_push,latest)

run_docker:
	docker compose -f docker/docker-compose.yml --env-file docker/.env up

run:
	${BUILD_DIR}/$(SVC)
