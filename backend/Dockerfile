FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /gas-drainage-server \
    ./cmd/

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /gas-drainage-server .
COPY --from=builder /build/config/optimizer.json ./config/optimizer.json
COPY frontend/static ./frontend/static

EXPOSE 8080 6060

ENV STATIC_DIR=/app/frontend/static
ENV OPTIMIZER_CONFIG=/app/config/optimizer.json

ENTRYPOINT ["/app/gas-drainage-server"]
