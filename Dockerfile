# openbox control plane — multi-stage, pure-Go (CGO off), minimal final image.
#
# Build:  docker build -t openbox-control .
# Run:    docker run -p 8080:8080 \
#           -e OPENBOX_ADDR=:8080 \
#           -e OPENBOX_PUBLIC_URL=https://opbx.net \
#           -e OPENBOX_DB=postgres://… \
#           -e OPENBOX_CA_KEY="$(openbox ca-keygen)" \
#           openbox-control
#
# The container is stateless: the database lives in Postgres (Neon) and the CA
# key arrives as a secret env var, so the ephemeral container disk holds nothing
# durable — which is exactly what Cloudflare Containers requires.

# ---- build ----
FROM golang:1.26 AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/openbox ./cmd/openbox

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/openbox /usr/local/bin/openbox

ENV OPENBOX_ADDR=:8080
EXPOSE 8080

# Distroless has no shell; exec the binary directly. Config comes from env.
ENTRYPOINT ["/usr/local/bin/openbox"]
CMD ["control"]
