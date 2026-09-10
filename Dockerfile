# Build/check container for cbzr. Used with:
#   nsc build --build-arg VERSION=0.2.0 --output-local=dist .
FROM golang:1.26 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy && gofmt -l . && go vet ./...
ENV CGO_ENABLED=0
RUN LDFLAGS="-s -w -X main.version=${VERSION#v}" && \
    GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o /out/cbzr-darwin-arm64 . && \
    GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o /out/cbzr-darwin-amd64 . && \
    GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o /out/cbzr-linux-amd64 . && \
    GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o /out/cbzr-linux-arm64 . && \
    GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o /out/cbzr-windows-amd64.exe .

FROM scratch
COPY --from=build /out/ /
COPY --from=build /src/go.mod /src/go.sum /
