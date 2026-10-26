# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Local development: rebuilds and restarts on file changes.
FROM base AS dev
RUN go install github.com/air-verse/air@latest
COPY . .
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/duradent ./cmd/duradent

# One image for every mode: api | worker | outbox-relay | migrate.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/duradent /duradent
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/duradent"]
CMD ["api"]
