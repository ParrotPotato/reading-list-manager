# Catalogue in a container. The database is not in the image: it lives in a
# folder mounted at /data, so it outlives every rebuild. See compose.yaml.

# Compile on the build machine's own architecture even when targeting another.
# There is no cgo, so cross-compiling needs nothing but GOARCH, and emulating
# amd64 on an arm64 laptop to compile SQLite takes many times as long.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /catalogue .

# Templates, CSS and script are embedded, so the binary is all that ships. This
# base adds the CA certificates that fetching a pasted link's title needs.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /catalogue /catalogue
EXPOSE 8080

# Every interface *inside the container*: the app's default, localhost, cannot
# be reached through a published port. compose.yaml decides what reaches this.
# An entrypoint rather than a command, so account flags can simply be appended.
ENTRYPOINT ["/catalogue", "-addr", "0.0.0.0:8080", "-db", "/data/reading-list.db"]
