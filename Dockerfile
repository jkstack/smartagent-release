FROM golang:latest

ADD go.mod \
    go.sum \
    release.go \
    /build/

WORKDIR /build
RUN go build -o /usr/bin/release release.go

ENTRYPOINT ["/usr/bin/release"]