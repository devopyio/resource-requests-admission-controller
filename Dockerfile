FROM golang:1-alpine AS build

RUN apk update && apk add make git gcc musl-dev

ADD . /go/src/github.com/devopyio/resource-requests-admission-controller

WORKDIR /go/src/github.com/devopyio/resource-requests-admission-controller

ENV GO111MODULE on
RUN make build
RUN mv resource-requests-admission-controller /resource-requests-admission-controller

FROM alpine:latest

RUN apk add --no-cache ca-certificates && mkdir /app
RUN adduser app -u 1001 -g 1001 -s /bin/false -D app

COPY --from=build /resource-requests-admission-controller /usr/bin
RUN chown -R app /usr/bin/resource-requests-admission-controller

USER app
ENTRYPOINT ["/usr/bin/resource-requests-admission-controller"]

