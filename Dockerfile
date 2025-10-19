# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.25 AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src

# Prime module cache
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -buildvcs=false \
    -o /out/recomma ./cmd/recomma

# Small, nonroot runtime
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /out/recomma /usr/local/bin/recomma
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/recomma"]