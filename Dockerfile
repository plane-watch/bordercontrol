FROM golang:1.20.4 AS builder
COPY pw_bordercontrol/ /src/pw_bordercontrol
WORKDIR /src/pw_bordercontrol
RUN go mod tidy
RUN go build -v -o ./bordercontrol cmd/bordercontrol/main.go

FROM alpine:3
COPY --from=builder --chmod=755 /src/pw_bordercontrol/bordercontrol /bordercontrol
ENTRYPOINT [ "/bordercontrol" ]
