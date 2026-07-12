# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags "-s -w" -o /out/spore .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10002 spore \
    && adduser -D -u 10002 -G spore spore

COPY --from=builder /out/spore /usr/local/bin/spore

USER spore
WORKDIR /home/spore

ENTRYPOINT ["spore"]
