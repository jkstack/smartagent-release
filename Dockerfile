ARG GOPROXY=

FROM golang:latest AS build

ARG GOPROXY
ENV GOPROXY=${GOPROXY}

ADD go.mod \
    go.sum \
    release.go \
    /build/

WORKDIR /build
RUN go build -o /usr/bin/release release.go

FROM debian:stable-slim

COPY --from=build /usr/bin/release /usr/bin/release

ENTRYPOINT ["/usr/bin/release"]