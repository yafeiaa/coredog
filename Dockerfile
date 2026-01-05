FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /coredog cmd/main.go

FROM alpine:3.19
WORKDIR /
# Install debugging tools
RUN apk add --no-cache \
    bash \
    curl \
    wget \
    netcat-openbsd \
    busybox-extras \
    vim \
    tree \
    strace \
    tcpdump \
    file \
    ca-certificates \
    && rm -rf /var/cache/apk/*

COPY --from=builder /coredog /coredog

ENTRYPOINT ["/coredog"]
