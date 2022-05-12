FROM golang:latest

RUN go build -o /usr/bin/release release.go

ENTRYPOINT /usr/bin/release