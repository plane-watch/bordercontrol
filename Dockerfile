FROM golang:1.20.4
COPY pw_bordercontrol/ /src/pw_bordercontrol
WORKDIR /src/pw_bordercontrol
RUN go mod tidy
