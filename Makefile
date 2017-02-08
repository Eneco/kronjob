FOLDERS = ./cmd/... ./pkg/...
BUILD_DIR ?= build

# get version info from git's tags
GIT_COMMIT := $(shell git rev-parse HEAD)
GIT_TAG := $(shell git describe --tags --dirty 2>/dev/null)
VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null)

# inject version info into version vars
LD_RELEASE_FLAGS += -X github.com/eneco/kronjob/src/kronjob.GitCommit=${GIT_COMMIT}
LD_RELEASE_FLAGS += -X github.com/eneco/kronjob/src/kronjob.GitTag=${GIT_TAG}
LD_RELEASE_FLAGS += -X github.com/eneco/kronjob/src/kronjob.SemVer=${VERSION}

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

run:
	-kubectl delete namespace kronjob-test
	make docker publish_docker apply

apply:
	-kubectl create namespace kronjob-test
	kubectl apply -f examples/random-fail.yaml --namespace=kronjob-test
	kubectl get pods --namespace kronjob-test

# builds a statically linked binary for linux-amd64
dockerbinary:
	cd cmd && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LD_RELEASE_FLAGS)" -o ../$(BUILD_DIR)/kronjob;

docker: dockerbinary
	cp docker/* ./$(BUILD_DIR)/
	docker build -t eneco/kronjob ./$(BUILD_DIR)/
	docker tag eneco/kronjob eneco/kronjob:latest
	docker tag eneco/kronjob eneco/kronjob:$(GIT_TAG)


publish_docker: docker
	docker push eneco/kronjob:latest
	docker push eneco/kronjob:$(GIT_TAG)
