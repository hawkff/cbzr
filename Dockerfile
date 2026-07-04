# Build/check container for cbzr. Used with:
#   nsc build --output-local=dist .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy && gofmt -l . && go vet ./...
RUN CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o /out/cbzr-darwin-arm64 .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/cbzr-linux-amd64 .

FROM scratch
COPY --from=build /out/ /
COPY --from=build /src/go.mod /src/go.sum /
