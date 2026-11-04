# syntax=docker/dockerfile:1

FROM golang:1.26.2-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/devkit ./cmd/devkit
RUN CGO_ENABLED=0 go build -trimpath -o /out/provisor-fetch ./cmd/provisor-fetch

FROM gcr.io/distroless/static:latest
WORKDIR /srv
COPY --from=build /out/devkit /out/provisor-fetch /usr/bin/
COPY --from=build /src/.devkeys/keys.json .devkeys/keys.json
