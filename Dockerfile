# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags "-s -w" -o /out/spore .

FROM alpine:3.20 AS runtime

# git backs memory revision history; without it writes still succeed, unversioned.
RUN apk add --no-cache ca-certificates tzdata git \
    && addgroup -g 10002 spore \
    && adduser -D -u 10002 -G spore spore \
    && mkdir -p /data/memory \
    && chown -R spore:spore /data

COPY --from=builder /out/spore /usr/local/bin/spore

# Memory must live on a mounted volume, or it resets on every redeploy.
# No VOLUME declaration: /data is bind-mounted by the stack, and declaring
# /data/memory here would layer an anonymous volume over that bind mount.
ENV MEMORY_DIR=/data/memory
EXPOSE 8080

USER spore
WORKDIR /home/spore

ENTRYPOINT ["spore"]
