# syntax=docker/dockerfile:1.4

FROM golang:1.21-alpine3.18 AS build-dev
WORKDIR /go/src/app
COPY --link go.mod go.sum ./
RUN apk --update add --no-cache upx gcc musl-dev || \
    go version && \
    go mod download
COPY --link . .
RUN CGO_ENABLED=1 go install -buildvcs=false -trimpath -ldflags '-w -s -extldflags "-static"'
RUN [ -e /usr/bin/upx ] && upx /go/bin/feed2bsky || echo
FROM scratch
COPY --link --from=build-dev /go/bin/feed2bsky /go/bin/feed2bsky
COPY --from=build-dev /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
CMD ["/go/bin/feed2bsky"]
