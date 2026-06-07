FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/muxmail ./cmd/muxmail

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
