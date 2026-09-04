# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o vibemonitor .

# Production stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/vibemonitor /app/vibemonitor

VOLUME [ "/app/data" ]
EXPOSE 25774

ENV VIBEMONITOR_LISTEN="0.0.0.0:25774"
ENV VIBEMONITOR_DATA="/app/data/vibemonitor-data.json"

ENTRYPOINT ["/app/vibemonitor", "server"]
