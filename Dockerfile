# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26 AS build
WORKDIR /src

# No third-party deps, so this is effectively a no-op — but it keeps the
# module graph cached in its own layer if dependencies are ever added.
COPY go.mod ./
RUN go mod download

COPY . .
# Static, CGO-free binary so it runs on a scratch/distroless base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/goldstream ./cmd/goldstream

# ---- runtime stage ----
# distroless/static ships CA certificates (needed for the HTTPS call to
# goldapi.io) and runs as a non-root user by default.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bin/goldstream /goldstream
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/goldstream"]
