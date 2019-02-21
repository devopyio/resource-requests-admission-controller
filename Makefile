DATE := $(shell date +%FT%T%z)
USER := $(shell whoami)
GIT_HASH := $(shell git --no-pager describe --tags --always)
BRANCH := $(shell git branch | grep \* | cut -d ' ' -f2)
DOCKER_IMAGE := resource-requests-admission-controller
GO111MODULE := on
all: go-deps go-test go-build docker-push

go-deps:
	go get -t ./...

go-build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -X main.version=$(GIT_HASH) -X main.date="$(DATE)" -X main.branch=$(BRANCH) -X main.revision=$(GIT_HASH) -X main.user=$(USER) -extldflags "-static"' .

.PHONY: go-test
go-test:
	$(BUILDENV) go test $(TESTFLAGS) ./...

docker-build:
	docker build . -t $(DOCKER_IMAGE)

docker-login:
	docker login -u $(DOCKER_USERNAME) -p $(DOCKER_PASSWORD)

docker-push: docker-build docker-login
	docker tag $(DOCKER_IMAGE) $(DOCKER_USERNAME)/$(DOCKER_IMAGE):$(GIT_HASH)
	docker push $(DOCKER_USERNAME)/$(DOCKER_IMAGE):$(GIT_HASH)
