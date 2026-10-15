package db

import (
	"context"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"vpnmaster/ingest/internal/domain"
)

// 本包只暴露 Store 方法与领域实体, 不向调用方泄漏 gorm/驱动类型。
// 驱动可换: sqlite(默认, 纯 Go 无 CGO) → postgres(多实例时), 业务代码零改动。

// Upstream 是一条上游订阅来源。
type Upstream struct {
	ID        int64     `gorm:"primaryKey"`
	Name      string    `gorm:"uniqueIndex"` // 来源标签(如 real-sub-vpn4zx)
	URLMasked string    // 脱敏展示(保留域名, 隐去 token); 真实 URL 只存 env/secret
	LastOKAt  time.Time
	LastErr   string
	CreatedAt time.Time
}

// NodeRow 是节点库中的一行(领域 Node + 生命周期字段)。
// 去重键 Fingerprint 跨上游唯一 —— 同源双拉不会双份路由。
type NodeRow struct {
	ID          int64  `gorm:"primaryKey"`
	Fingerprint string `gorm:"uniqueIndex"`
	Address     string
	Port        int
	Protocol    string
	StreamJSON  string // xraygen 直填 outbound 的传输层 JSON(含凭证)
	SNI         string
	Tag         string
	Source      string // 来源 Upstream.Name
	Status      string // domain.Node* 常量
	LatencyMS   int
	LastSeenAt  time.Time
	StaleCount  int // 连续未出现次数, 达阈值删除
}

// Event 是状态/生命周期事件(剔除/回池/拉取失败), admin 时间线数据源。
type Event struct {
	ID     int64 `gorm:"primaryKey"`
	Kind   string // "evict" | "recover" | "stale" | "pull_err"
	NodeFP string
	From   string
	To     string
	Detail string
	At     time.Time
}

// Store 是所有持久化操作的唯一入口(repository 接口, 驱动无关)。
type Store struct {
	db *gorm.DB
}

// NewStore 打开并初始化存储。driver: "sqlite"(默认, dsn=文件路径) | "postgres"(dsn=DSN)。
func NewStore(driver, dsn string) (*Store, error) {
	var g *gorm.DB
	var err error
	switch driver {
	case "", "sqlite":
		g, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	case "postgres":
		g, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	default:
		return nil, gorm.ErrInvalidDB
	}
	if err != nil {
		return nil, err
	}
	if err := g.AutoMigrate(&Upstream{}, &NodeRow{}, &Event{}); err != nil {
		return nil, err
	}
	return &Store{db: g}, nil
}

// maskURL 脱敏: 保留域名, 隐去 query token 后半段。
func maskURL(raw string) string {
	if i := len(raw) - 12; i > 0 {
		return raw[:i] + "…(masked)"
	}
	return "(masked)"
}

// AddUpstream 登记来源; 已存在返回 created=false。
func (s *Store) AddUpstream(ctx context.Context, name, rawURL string) (bool, error) {
	var existing Upstream
	err := s.db.WithContext(ctx).Where("name = ?", name).First(&existing).Error
	switch {
	case err == nil:
		return false, nil
	case err != gorm.ErrRecordNotFound:
		return false, err
	}
	u := Upstream{Name: name, URLMasked: maskURL(rawURL), CreatedAt: time.Now()}
	if err := s.db.WithContext(ctx).Create(&u).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ListUpstreams(ctx context.Context) ([]Upstream, error) {
	var out []Upstream
	if err := s.db.WithContext(ctx).Order("name").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MarkUpstreamOK(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Model(&Upstream{}).Where("id = ?", id).
		Updates(map[string]any{"last_ok_at": time.Now(), "last_err": ""}).Error
}

func (s *Store) MarkUpstreamErr(ctx context.Context, id int64, errText string) error {
	return s.db.WithContext(ctx).Model(&Upstream{}).Where("id = ?", id).
		Update("last_err", errText).Error
}

// UpsertNodes 写入一轮拉取到的节点: 返回 (新增, 已存在) 指纹列表。幂等。
func (s *Store) UpsertNodes(ctx context.Context, source string, nodes []domain.Node) (added, existed []string, err error) {
	for _, n := range nodes {
		fp := domain.NewFingerprint(n)
		var existing NodeRow
		eerr := s.db.WithContext(ctx).Where("fingerprint = ?", fp).First(&existing).Error
		switch {
		case eerr == gorm.ErrRecordNotFound:
			row := NodeRow{
				Fingerprint: fp,
				Address:     n.Address,
				Port:        n.Port,
				Protocol:    string(n.Protocol),
				StreamJSON:  n.Stream.StreamJSON,
				SNI:         n.Stream.SNI,
				Tag:         n.Tag,
				Source:      source,
				Status:      string(domain.NodePending),
				LastSeenAt:  time.Now(),
			}
			if cerr := s.db.WithContext(ctx).Create(&row).Error; cerr != nil {
				return nil, nil, cerr
			}
			added = append(added, fp)
		case eerr == nil:
			if uerr := s.db.WithContext(ctx).Model(&existing).Updates(map[string]any{
				"address": n.Address, "port": n.Port, "protocol": string(n.Protocol),
				"stream_json": n.Stream.StreamJSON, "sni": n.Stream.SNI, "tag": n.Tag,
				"source": source, "last_seen_at": time.Now(), "stale_count": 0,
			}).Error; uerr != nil {
				return nil, nil, uerr
			}
			existed = append(existed, fp)
		default:
			return nil, nil, eerr
		}
	}
	return added, existed, nil
}

// MarkStale: 本轮来源未再出现的节点 stale_count+1, 达阈值删除并记事件。
func (s *Store) MarkStale(ctx context.Context, source string, seen []string, threshold int) (deleted []string, err error) {
	seenSet := make(map[string]bool, len(seen))
	for _, fp := range seen {
		seenSet[fp] = true
	}
	var rows []NodeRow
	if err := s.db.WithContext(ctx).Where("source = ?", source).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		row := &rows[i]
		if seenSet[row.Fingerprint] {
			continue
		}
		row.StaleCount++
		if row.StaleCount >= threshold {
			if derr := s.db.WithContext(ctx).Delete(row).Error; derr != nil {
				return nil, derr
			}
			deleted = append(deleted, row.Fingerprint)
			_ = s.RecordEvent(ctx, Event{Kind: "stale", NodeFP: row.Fingerprint, From: row.Status, To: "deleted", Detail: source})
		} else {
			if uerr := s.db.WithContext(ctx).Model(&NodeRow{}).
				Where("id = ?", row.ID).Updates(map[string]any{"stale_count": row.StaleCount}).Error; uerr != nil {
				return nil, uerr
			}
		}
	}
	return deleted, nil
}

func (s *Store) SetNodeStatus(ctx context.Context, fingerprint, from, to string) error {
	if err := s.db.WithContext(ctx).Model(&NodeRow{}).Where("fingerprint = ?", fingerprint).
		Update("status", to).Error; err != nil {
		return err
	}
	kind := "evict"
	if to == string(domain.NodeActive) {
		kind = "recover"
	}
	return s.RecordEvent(ctx, Event{Kind: kind, NodeFP: fingerprint, From: from, To: to})
}

func (s *Store) SetLatency(ctx context.Context, fingerprint string, ms int) error {
	return s.db.WithContext(ctx).Model(&NodeRow{}).Where("fingerprint = ?", fingerprint).
		Update("latency_ms", ms).Error
}

// Evict 手动摘除(admin-web 按钮, 4.4): active→evicted, 记事件, 返回是否发生转移。
func (s *Store) Evict(ctx context.Context, fingerprint string) (bool, error) {
	return s.transition(ctx, fingerprint, string(domain.NodeActive), string(domain.NodeEvicted))
}

// Recover 手动回池(admin-web 按钮, 4.4): evicted→active, 记事件。
func (s *Store) Recover(ctx context.Context, fingerprint string) (bool, error) {
	return s.transition(ctx, fingerprint, string(domain.NodeEvicted), string(domain.NodeActive))
}

// transition 条件转移: 仅当当前状态为 from 时写入 to。返回是否实际转移。
func (s *Store) transition(ctx context.Context, fingerprint, from, to string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&NodeRow{}).
		Where("fingerprint = ? AND status = ?", fingerprint, from).
		Update("status", to)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	kind := "evict"
	if to == string(domain.NodeActive) {
		kind = "recover"
	}
	if err := s.RecordEvent(ctx, Event{Kind: kind, NodeFP: fingerprint, From: from, To: to}); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Store) ListNodes(ctx context.Context, status string) ([]NodeRow, error) {
	q := s.db.WithContext(ctx)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []NodeRow
	if err := q.Order("latency_ms").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) RecordEvent(ctx context.Context, e Event) error {
	if e.ID == 0 {
		e.At = time.Now()
	}
	return s.db.WithContext(ctx).Create(&e).Error
}

func (s *Store) ListEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []Event
	if err := s.db.WithContext(ctx).Order("id desc").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Close 释放底层连接（测试与优雅退出用）。
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
