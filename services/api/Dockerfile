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

# Amazon RDS CA bundle: the API verifies the database certificate (MYSQL_CA_FILE).
FROM alpine:3.22 AS rds-ca
ADD https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem /rds-ca.pem
RUN grep -q "BEGIN CERTIFICATE" /rds-ca.pem && chmod 0444 /rds-ca.pem

# One image for every mode: api | worker | outbox-relay | migrate.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/duradent /duradent
COPY --from=rds-ca /rds-ca.pem /etc/ssl/rds-ca.pem
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/duradent"]
CMD ["api"]
