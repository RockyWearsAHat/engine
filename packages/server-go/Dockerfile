# Build stage
FROM golang:1.26.1-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the server
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o engine-server .

# Runtime stage
FROM alpine:3.20

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates git

# Copy binary from builder
COPY --from=builder /app/engine-server .

# Expose port (default 24444, can be overridden with PORT env var)
EXPOSE 24444

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:24444/health || exit 1

# Run the server
CMD ["./engine-server"]
