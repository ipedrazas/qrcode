# syntax=docker/dockerfile:1

# Build on the native platform and cross-compile for the target, so
# multi-arch images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/qrsvc ./cmd/qrsvc

# distroless/static: no shell, no package manager, no libc. The service
# writes nothing to disk, so run it with a read-only root filesystem.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/qrsvc /qrsvc
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/qrsvc"]
