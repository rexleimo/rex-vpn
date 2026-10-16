#!/bin/bash
# user-center (authkit + 订阅镜像) 独立数据库; marzban 继续用默认库。
# 首次启动(空数据卷)由 docker-entrypoint-initdb.d 执行。
set -euo pipefail
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" <<-SQL
  CREATE DATABASE rexvpn OWNER "$POSTGRES_USER";
SQL
