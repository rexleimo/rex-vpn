# vpn-master 开发命令 (开发环境 = Docker, 与 Dokploy 线上同构)
COMPOSE := docker compose -f deploy/docker-compose.yml
DEVCMP  := $(COMPOSE) -f deploy/docker-compose.dev.yml

.PHONY: env up down dev ps logs shell test-image

env:            # 首次: 生成开发 env
	cp -n deploy/env.example deploy/.env || true

up:             # 基础栈 (postgres + marzban + xray-edge)
	$(COMPOSE) up -d

dev:            # 全量开发态 (含 ingest 热重载 + dev-shell)
	$(DEVCMP) up -d --build

down:
	$(COMPOSE) down

ps:
	$(DEVCMP) ps

logs:           # 跟踪全部日志
	$(DEVCMP) logs -f

shell:          # 进 dev-shell (repo 已挂载, 可 docker exec 调各服务)
	$(DEVCMP) exec dev-shell bash

build-ingest:   # 产出线上镜像 (Dokploy 用)
	docker build -t vpn-master/ingest:latest services/ingest

build-edge:     # xray-edge 镜像 (xray + watcher)
	docker build -t vpn-master/xray-edge:latest deploy/edge

edge:           # compose 里 xray-edge build 默认指向这里
	docker build -t vpn-master/xray-edge:latest deploy/edge

test-go:        # 本地跑 ingest 单测 (或进 dev 容器)
	cd services/ingest && go test ./...

e2e-edge:       # work-5.4: 全链路 xray-edge 出口断言 (需 make dev 起栈 + 真实 sub)
	scripts/e2e-edge.sh
