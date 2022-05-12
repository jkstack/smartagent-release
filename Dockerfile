FROM golang:latest

ADD release.go /release.go

ENTRYPOINT entrypoint.sh