VERSION ?= 1.0.0
IMAGE ?= paystar/payment-exporter
GO ?= go
GOPROXY ?= https://proxy.golang.org,direct
DOCKER_BUILD_FLAGS ?=
REVISION := $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.revision=$(REVISION) -X main.buildDate=$(BUILD_DATE)

.PHONY: build test vet check docker docker-mtr save clean
build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o bin/payment-exporter ./cmd/payment-exporter
test:
	$(GO) test -race -count=1 ./...
vet:
	$(GO) vet ./...
check: test vet
docker:
	docker build $(DOCKER_BUILD_FLAGS) --target default --build-arg 'GOPROXY=$(GOPROXY)' --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE):$(VERSION) .
docker-mtr:
	docker build $(DOCKER_BUILD_FLAGS) --target mtr --build-arg 'GOPROXY=$(GOPROXY)' --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE):$(VERSION)-mtr .
save:
	mkdir -p dist
	docker save $(IMAGE):$(VERSION) $(IMAGE):$(VERSION)-mtr | gzip > dist/payment-exporter-$(VERSION)-images.tar.gz
clean:
	rm -rf bin dist
