FROM golang:1.25-bookworm AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        gcc libc6-dev pkg-config libfreetype-dev \
        zlib1g-dev libbz2-dev libpng-dev libbrotli-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux CGO_LDFLAGS="$(pkg-config --static --libs freetype2)" \
    go build -trimpath -tags="netgo,osusergo" -ldflags="-s -w -extldflags '-static'" \
    -o /out/zectrix ./cmd/zectrix

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/zectrix /usr/local/bin/zectrix

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zectrix"]
CMD ["--help"]
