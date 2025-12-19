FROM golang:1.23-alpine AS builder

# Install git to clone rmapi repository
RUN apk add --no-cache git

WORKDIR /src

# Clone the latest version of rmapi from your fork
# This ensures we always get the latest version from francoisWeber/rmapi master branch
RUN git clone --depth 1 https://github.com/francoisWeber/rmapi.git /rmapi

# Copy go mod files for dependency caching
COPY go.mod go.sum ./

# Download dependencies (this layer will be cached unless go.mod/go.sum change)
# Go will use the local /rmapi directory via the replace directive
RUN go mod download

COPY . ./

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

