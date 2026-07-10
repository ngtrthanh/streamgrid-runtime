# StreamGrid Edge Server - Multi-stage build
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /streamgrid-edge ./edge/cmd/

# Runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /streamgrid-edge /usr/local/bin/streamgrid-edge
COPY web/ /app/web/

WORKDIR /app

EXPOSE 8080 4433/udp 4433/tcp

ENTRYPOINT ["streamgrid-edge"]
CMD ["-ws-addr", ":8080", "-rate", "2"]
