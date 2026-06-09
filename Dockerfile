# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:20.19-alpine AS admin-build

WORKDIR /src/web/admin
COPY web/admin/package*.json ./
COPY web/admin/.npmrc ./
RUN npm ci
COPY web/admin ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build

WORKDIR /src
ENV CGO_ENABLED=0
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/api/admin_dist && mkdir -p internal/api/admin_dist
COPY --from=admin-build /src/web/admin/dist/ internal/api/admin_dist/
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/muxmail ./cmd/muxmail

FROM alpine:3.20

RUN apk add --no-cache ca-certificates
RUN addgroup -S muxmail && adduser -S -G muxmail muxmail \
    && mkdir -p /etc/muxmail /var/lib/muxmail \
    && chown -R muxmail:muxmail /etc/muxmail /var/lib/muxmail
WORKDIR /app
COPY --from=build /out/muxmail /usr/local/bin/muxmail

EXPOSE 8080
USER muxmail
VOLUME ["/etc/muxmail", "/var/lib/muxmail"]

ENTRYPOINT ["muxmail"]
CMD ["serve", "-c", "/etc/muxmail/config.yaml"]
