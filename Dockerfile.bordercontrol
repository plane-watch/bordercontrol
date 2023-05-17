FROM golang:1.20.4 AS builder
COPY pw_bordercontrol/ /src/pw_bordercontrol
WORKDIR /src/pw_bordercontrol
RUN go mod tidy
RUN go build ./...
RUN go install ./...
ENTRYPOINT [ "bordercontrol" ]