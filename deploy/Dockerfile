# syntax=docker/dockerfile:1

# The console, built once on the build platform: its output is the same for
# every target, and the Go build embeds it.
FROM --platform=$BUILDPLATFORM node:26-alpine AS console
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
# The pnpm the workspace names in packageManager, so its version has one home.
RUN npm install --global "$(node -p 'require("./package.json").packageManager')"
COPY web/apps/console/package.json ./apps/console/
COPY web/packages/api/package.json ./packages/api/
COPY web/e2e/package.json ./e2e/
RUN pnpm install --frozen-lockfile --filter @flagnetic/console...
COPY web/ ./
RUN pnpm --filter @flagnetic/console build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
ENV GOTOOLCHAIN=auto
# The evaluator's go.mod comes along: go.mod replaces the evaluator with ./eval.
COPY go.mod go.sum ./
COPY eval/go.mod ./eval/
RUN go mod download
COPY . .
COPY --from=console /src/internal/infra/in/http/console/dist/ ./internal/infra/in/http/console/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w -X github.com/azverinus/flagnetic/internal/pkg/buildinfo.version=${VERSION}" \
      -o /out/flagnetic ./cmd/flagnetic

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/flagnetic /usr/local/bin/flagnetic
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/flagnetic"]
CMD ["serve"]
# The public listener (FLAGNETIC_LISTEN_ADDR: APIs, console, probes) and the
# metrics listener (FLAGNETIC_METRICS_LISTEN_ADDR).
EXPOSE 8080 9090
