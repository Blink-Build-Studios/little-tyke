FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/Blink-Build-Studios/little-tyke/cmd/little-tyke/cmd/version.Version=${VERSION}" \
    -o /little-tyke ./cmd/little-tyke

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /little-tyke /usr/local/bin/little-tyke

ENTRYPOINT ["little-tyke"]
CMD ["serve"]
