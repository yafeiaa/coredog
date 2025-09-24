IMAGE_NAME=coredog
DOCKER_REPO=coderflyfyf
VERSION=$(shell git describe --tags --abbrev=0 2>/dev/null || echo 'latest')

.PHONY: build
build:
	docker build -t $(DOCKER_REPO)/$(IMAGE_NAME):$(VERSION) .

.PHONY: push
push: build
	docker push $(DOCKER_REPO)/$(IMAGE_NAME):$(VERSION)
