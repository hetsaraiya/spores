# syntax=docker/dockerfile:1.7

# ---- builder: compile a static binary -------------------------------------- #
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Resolve modules first so this layer caches unless go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go, statically linked so it runs on a minimal runtime image.
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags "-s -w" -o /out/spore .

# ---- runtime: minimal, non-root -------------------------------------------- #
FROM alpine:3.20 AS runtime

# TLS roots (Slack, GitHub, E2B, LangSmith, the OpenAI gateway) + tzdata for TZ.
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10002 spore \
    && adduser -D -u 10002 -G spore spore \
    && mkdir -p /data /auth \
    && chown -R spore:spore /data /auth

COPY --from=builder /out/spore /usr/local/bin/spore

# Long-term memory persists here; mount a volume over /data (see compose).
ENV MEMORY_DIR=/data/memory
VOLUME ["/data"]

USER spore
WORKDIR /home/spore

ENTRYPOINT ["spore"]
