ARG GO_VERSION=1.27.1-alpine
ARG ALPINE_VERSION=3.21

FROM golang:${GO_VERSION} AS builder

ARG PKG=./cmd/tracker

WORKDIR /build

COPY ./go.mod ./go.sum ./
RUN go mod download

COPY ./ ./

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ${PKG}

FROM alpine:${ALPINE_VERSION}

RUN apk --no-cache add ca-certificates \
    && addgroup -S gbtracker \
    && adduser -S -u 10000 -G gbtracker gbtracker \
    && mkdir -p /data \
    && chown -R gbtracker:gbtracker /data

COPY --from=builder /out/service /usr/local/bin/service

USER gbtracker
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/service"]
