# vpn-master / ingest (线上镜像)
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ingest ./cmd/ingest

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ingest /usr/local/bin/ingest
# 约定挂载: /data (ingest.db) 与 /configs (xray-edge config, 与 xray-edge 容器共享卷)
ENTRYPOINT ["/usr/local/bin/ingest"]
