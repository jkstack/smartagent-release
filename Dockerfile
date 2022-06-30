FROM golang:latest AS build

ADD go.mod \
    go.sum \
    release.go \
    /build/

WORKDIR /build
RUN go build -o /usr/bin/release release.go

FROM debian:stable-slim

COPY --from=build /usr/bin/release /usr/bin/release

RUN apt-get update && apt-get upgrade -y && \
    apt-get install -y make ca-certificates

ENTRYPOINT ["/usr/bin/release"]