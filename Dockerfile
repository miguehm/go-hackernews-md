# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27.3
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build --chown=nonroot:nonroot /out/data /data
COPY --from=build /out/server /server
ENV PORT=8982
ENV DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8982
USER nonroot:nonroot
ENTRYPOINT ["/server"]
