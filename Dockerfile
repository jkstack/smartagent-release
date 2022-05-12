FROM golang:latest

ADD entrypoint.sh /entrypoint.sh
ADD release.go /release.go

ENTRYPOINT /entrypoint.sh