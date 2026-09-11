FROM golang:1.27-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ethol-autopresence ./cmd/ethol-autopresence
RUN mkdir -p /app/data && chown -R 65532:65532 /app

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app
COPY --from=builder --chown=65532:65532 /app/data /app/data
COPY --from=builder /ethol-autopresence /app/ethol-autopresence
USER nonroot:nonroot
ENTRYPOINT ["/app/ethol-autopresence"]
