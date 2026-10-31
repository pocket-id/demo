# Cross-compiles on the build machine, so a multi-platform build doesn't compile under emulation
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /demo .

# Runs as root because it needs the Docker socket
FROM gcr.io/distroless/static
COPY --from=build /demo /demo
EXPOSE 8080
ENTRYPOINT ["/demo"]
