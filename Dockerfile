FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /jumpcloud-teleport-sync .

# ---

FROM alpine:3.20

LABEL org.opencontainers.image.source=https://github.com/moveaxlab/jumpcloud-teleport-sync

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /jumpcloud-teleport-sync /usr/local/bin/jumpcloud-teleport-sync

RUN adduser -D -u 1000 syncer
USER syncer

ENTRYPOINT ["jumpcloud-teleport-sync"]
