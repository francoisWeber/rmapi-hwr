FROM golang:1.23-alpine AS builder

# Copy rmapi directory (needed for replace directive: ../rmapi)
# This goes to /rmapi to match the replace directive path from /src
COPY rmapi/ /rmapi/

WORKDIR /src

# Copy go mod files for dependency caching
COPY rmapi-hwr/go.mod rmapi-hwr/go.sum ./
# Download dependencies (this layer will be cached unless go.mod/go.sum change)
RUN go mod download

# Copy the rest of the rmapi-hwr source code
COPY rmapi-hwr/ ./

# Build the server application
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server

FROM alpine:latest

RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /src/server /app/server

# Create output directory
RUN mkdir -p /tmp/rmapi-hwr-output

# Expose the server port
EXPOSE 8082

# Run the server
CMD ["/app/server"]

