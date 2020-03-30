ARG VERSION=undefined

FROM golang:1.14 AS builder
ARG VERSION

WORKDIR /k8s-spot-rescheduler

COPY *.go ./
COPY deploy deploy/
COPY metrics metrics/
COPY nodes nodes/
COPY scaler scaler/
COPY go.mod ./go.mod
COPY go.sum ./go.sum

ENV GO111MODULE=on

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-X main.VERSION=${VERSION}" -a -o k8s-spot-rescheduler github.com/pusher/k8s-spot-rescheduler

FROM alpine:3.9
RUN apk --no-cache add ca-certificates
WORKDIR /bin
COPY --from=builder /k8s-spot-rescheduler .

ENTRYPOINT ["/bin/k8s-spot-rescheduler"]
