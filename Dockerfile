FROM golang:1.20.4 AS builder
COPY pw_bordercontrol/ /src/pw_bordercontrol
WORKDIR /src/pw_bordercontrol
RUN go mod tidy
RUN go build -v -o ./bordercontrol cmd/bordercontrol/main.go

FROM scratch
COPY --from=builder /src/pw_bordercontrol/bordercontrol /bordercontrol
ENTRYPOINT [ "/bordercontrol" ]
