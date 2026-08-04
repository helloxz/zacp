# Multi-stage build for zacp backend (frontend can be added later).
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/zacp ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/zacp /app/zacp
EXPOSE 8080
ENTRYPOINT ["/app/zacp"]
