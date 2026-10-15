# vpn-master / ingest (线上镜像)
# build context = 仓库 services/ 目录 (含共享模块 webui), 见 compose context 与 Dockerfile 相对路径。
FROM golang:1.26-alpine AS build
WORKDIR /src
# 先拷 go.mod + 共享模块以便 go mod download
COPY webui/go.mod ./webui/go.mod
COPY ingest/go.mod ./ingest/go.mod
COPY ingest/go.sum ./ingest/go.sum
RUN cd ingest && go mod download
# 再拷源码
COPY webui/ ./webui/
COPY ingest/ ./ingest/
RUN cd ingest && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ingest ./cmd/ingest

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ingest /usr/local/bin/ingest
# 约定挂载: /data (ingest.db) 与 /configs (xray-edge config, 与 xray-edge 容器共享卷)
ENTRYPOINT ["/usr/local/bin/ingest"]
