FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/usagent ./cmd/usagent

FROM alpine:3.22 AS runtime

RUN adduser -S -u 10001 usagent && apk add --no-cache ca-certificates wget

ENV USAGENT_CONFIG=/etc/usagent/config.yaml \
    USAGENT_HOST=0.0.0.0 \
    USAGENT_PORT=8787

COPY --from=build /out/usagent /usr/local/bin/usagent
COPY config.example.yaml /etc/usagent/config.yaml

RUN mkdir -p /var/lib/usagent && chown -R usagent /var/lib/usagent
USER usagent

EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8787/healthz >/dev/null || exit 1

CMD ["usagent"]
