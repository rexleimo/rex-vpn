# user-center 镜像: 单二进制 Go SSR(无 Node 构建链)
# build context = 仓库 services/ 目录 (含共享模块 webui)。
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY webui/go.mod ./webui/go.mod
COPY user-center/go.mod ./user-center/go.mod
COPY user-center/go.sum ./user-center/go.sum
RUN cd user-center && go mod download
COPY webui/ ./webui/
COPY user-center/ ./user-center/
RUN cd user-center && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/user-center ./cmd/user-center

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/user-center /user-center
ENV USERCENTER_ADDR=:9200
EXPOSE 9200
ENTRYPOINT ["/user-center"]
