# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o video_downloader .

# Final stage
FROM alpine:latest

WORKDIR /app

# Install ffmpeg and CA certificates
RUN apk add --no-cache ffmpeg ca-certificates tzdata

# Copy the binary from the builder stage
COPY --from=builder /app/video_downloader .

# Create the downloads directory
RUN mkdir -p /app/downloads

# Expose the API port
EXPOSE 8080

# Run the binary
ENTRYPOINT ["/app/video_downloader"]
CMD ["--port", "8080"]
