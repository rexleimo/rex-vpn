// Package db 负责节点/上游/事件的持久化（work-3）。
// 数据访问: GORM（驱动可换: modernc.org/sqlite 默认, 预留 postgres），
// 本包只导出 repository 接口, 不向调用方泄漏驱动/ORM 类型, 保证迁移 Postgres 零业务改动。
// 表: upstreams / nodes(去重键=hash(addr|port|proto|stream|sni)) / events。
package db
