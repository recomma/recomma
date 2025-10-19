FROM golang:1.25 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/recomma ./cmd/recomma

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

COPY --from=builder /out/recomma /usr/local/bin/recomma

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/recomma"]
