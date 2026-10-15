// Package db 负责节点/上游/事件的持久化（work-3）。
// 存储: SQLite (modernc.org/sqlite, 纯 Go 无 CGO), 单文件 /data/ingest.db。
// 表: upstreams / nodes(去重键=hash(addr|port|proto|stream|sni)) / events。
package db
