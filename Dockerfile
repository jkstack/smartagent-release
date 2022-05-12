FROM golang:latest

ADD release.go /release.go

ENTRYPOINT go run /release.go .