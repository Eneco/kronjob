FOLDERS = ./cmd/... ./pkg/...
BUILD_DIR ?= build

# get version info from git's tags
GIT_COMMIT := $(shell git rev-parse HEAD)
GIT_TAG := $(shell git describe --tags --dirty 2>/dev/null)
VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null)

# inject version info into version vars
LD_RELEASE_FLAGS += -X github.com/eneco/kronjob/pkg/kronjob.GitCommit=${GIT_COMMIT}
LD_RELEASE_FLAGS += -X github.com/eneco/kronjob/pkg/kronjob.GitTag=${GIT_TAG}
LD_RELEASE_FLAGS += -X github.com/eneco/kronjob/pkg/kronjob.SemVer=${VERSION}

.PHONY: default bootstrap clean test build static docker

default: build

bootstrap:
	glide install -v

clean:
	rm -rf $(BUILD_DIR)

test:
	go test -cover $(FOLDERS)

build:
	cd cmd && go build -ldflags "$(LD_RELEASE_FLAGS)" -o ../$(BUILD_DIR)/kronjob

run: k8s_delete_ns docker apply

apply: k8s_create_ns k8s_apply

docker: docker_binary docker_image docker_publish

# Kubernetes commands
k8s_delete_ns:
	-kubectl delete namespace kronjob-test

k8s_create_ns:
	-kubectl create namespace kronjob-test

k8s_apply:
	kubectl apply -f examples/random-fail.yaml --namespace=kronjob-test

# Docker commands
docker_binary:
	cd cmd && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LD_RELEASE_FLAGS)" -o ../$(BUILD_DIR)/kronjob;

docker_image:
	cp docker/* ./$(BUILD_DIR)/
	docker build -t eneco/kronjob ./$(BUILD_DIR)/
	docker tag eneco/kronjob eneco/kronjob:latest
	docker tag eneco/kronjob eneco/kronjob:$(GIT_TAG)

docker_publish:
	docker push eneco/kronjob:latest
	docker push eneco/kronjob:$(GIT_TAG)
