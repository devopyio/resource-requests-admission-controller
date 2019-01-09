GIT_HASH := $(shell git rev-parse HEAD)

all: go-deps go-build docker-build
go-deps:
	go get -t ./...
go-build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-X main.Version=$(GIT_HASH) -extldflags "-static"' .
docker-build:
	docker build . -t resource-requests-admission-controller:latest
	docker tag resource-requests-admission-controller:latest devopyio/resource-requests-admission-controller:$(GIT_HASH)
	docker push devopyio/resource-requests-admission-controller:$(GIT_HASH)
