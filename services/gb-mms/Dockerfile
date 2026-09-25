ARG GO_VERSION=1.27.1-alpine
ARG ALPINE_VERSION=3.21
ARG UNRAR_VERSION=7.1.6

FROM alpine:${ALPINE_VERSION} AS unrar
ARG UNRAR_VERSION

RUN apk --no-cache add build-base curl

RUN curl -fsSL "https://www.rarlab.com/rar/unrarsrc-${UNRAR_VERSION}.tar.gz" -o /tmp/unrar.tar.gz \
    && tar xzf /tmp/unrar.tar.gz -C /tmp \
    && make -C /tmp/unrar -j"$(nproc)" LDFLAGS="-static -pthread" \
    && strip /tmp/unrar/unrar \
    && ! readelf -d /tmp/unrar/unrar 2>/dev/null | grep -q NEEDED

FROM golang:${GO_VERSION} AS builder

WORKDIR /build

COPY ./ ./

RUN CGO_ENABLED=0 go build -C services/gb-mms -trimpath -ldflags="-s -w" \
    -o /out/service ./cmd/gbmms

FROM alpine:${ALPINE_VERSION}

RUN apk --no-cache add 7zip bzip2 ca-certificates \
    && addgroup -S gbmms \
    && adduser -S -u 10000 -G gbmms gbmms \
    && mkdir -p /data \
    && chown -R gbmms:gbmms /data

COPY --from=unrar   /tmp/unrar/unrar /usr/local/bin/unrar
COPY --from=builder /out/service /usr/local/bin/gbmms

USER gbmms
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/gbmms"]
