FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG BUILD_TAGS=""

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build ${BUILD_TAGS:+-tags "$BUILD_TAGS"} -ldflags="-s -w" -o /ethold ./cmd/ethold
RUN mkdir -p /app/data && chown -R 65532:65532 /app

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app
COPY --from=builder --chown=65532:65532 /app/data /app/data
COPY --from=builder /ethold /app/ethold
USER nonroot:nonroot
ENTRYPOINT ["/app/ethold"]
CMD ["--state", "/app/data/attended_keys.json"]
