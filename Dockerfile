# ----------- Build Stage -----------
  FROM golang:1.23-alpine AS builder

  # Install Git and CA certs for module downloads
  RUN apk add --no-cache git ca-certificates
  
  WORKDIR /app
  
  # Copy go.mod and go.sum and download dependencies
  COPY go.mod go.sum ./
  RUN go mod download
  
  # Copy all source code and build the binary
  COPY . .
  RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o metadata-service .
  
  # ----------- Final Stage -----------
  FROM alpine:latest
  
  # Install CA certificates (for outbound HTTPS)
  RUN apk add --no-cache ca-certificates
  WORKDIR /root/
  
  # Copy the statically-linked binary from builder
  COPY --from=builder /app/metadata-service ./
  
  # Expose the default port
  EXPOSE 8080
  
  # Default environment variables
  ENV PORT=8080
  
  # Run the service
  ENTRYPOINT ["./metadata-service"]
  