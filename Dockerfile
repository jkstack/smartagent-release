FROM golang:latest

ADD entrypoint.sh /entrypoint.sh
ADD go.mod /go.mod
ADD release.go /release.go

ENTRYPOINT go run /release.go