ARG GOPROXY=
ARG APT_MIRROR=mirrors.ustc.edu.cn

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

ARG APT_MIRROR

COPY --from=build /usr/bin/release /usr/bin/release

RUN sed -i "s|deb.debian.org|$APT_MIRROR|g" /etc/apt/sources.list && \
    sed -i "s|security.debian.org|$APT_MIRROR|g" /etc/apt/sources.list && \
    apt-get update && apt-get upgrade -y && \
    apt-get install -y make

ENTRYPOINT ["/usr/bin/release"]