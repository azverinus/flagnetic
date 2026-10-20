# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w -X github.com/azverinus/flagnetic/internal/pkg/buildinfo.version=${VERSION}" \
      -o /out/flagnetic ./cmd/flagnetic

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/flagnetic /usr/local/bin/flagnetic
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/flagnetic"]
