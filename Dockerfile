FROM alpine:latest

RUN adduser rrac -s /bin/false -D rrac

COPY resource-requests-admission-controller /usr/bin

USER rrac

ENTRYPOINT ["/usr/bin/resource-requests-admission-controller"]
