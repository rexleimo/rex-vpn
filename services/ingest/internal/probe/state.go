package probe

import (
	"vpnmaster/ingest/internal/domain"
)

// 状态机(4.2): 转移表为纯函数, 便于全矩阵单测。
// 状态值来自 domain.Node*: pending / active / evicted。
// 语义:
//   - active  节点在池内, 探测正常。
//   - evicted 连续失败达阈值被摘除; 每 RETRY_PERIOD 探测一轮, 连续成功达阈值回池。
// 内存计数: consecFails / consecSucces 由 Prober 持有(重启清零, MVP 可接受)。

const (
	// EvictFails 连续失败 N 次 → 摘除(active→evicted)。
	EvictFails = 3
	// RecoverSucces 连续成功 N 次 → 回池(evicted→active)。
	RecoverSucces = 2
)

// ProbeOutcome 是一次探测结果。
type ProbeOutcome struct {
	OK    bool
	RTTMS int
}

// NextStatus 给定当前状态与探测结果, 返回 (新状态, 是否发生转移)。
// consecFails/consecSucces 为调用方维护的连续计数(已含本次)。
func NextStatus(cur domain.NodeStatus, ok bool, consecFails, consecSucces int) (domain.NodeStatus, bool) {
	switch cur {
	case domain.NodeActive:
		if ok {
			return cur, false
		}
		if consecFails >= EvictFails {
			return domain.NodeEvicted, true
		}
		return cur, false
	case domain.NodeEvicted:
		if ok && consecSucces >= RecoverSucces {
			return domain.NodeActive, true
		}
		return cur, false
	default: // pending: 尚未探测, 不在此表内转移(首探成功后由 Prober 置 active)
		if ok {
			return domain.NodeActive, true
		}
		return cur, false
	}
}

// IsInPool 判断状态是否参与 xraygen 出池(仅 active)。
func IsInPool(s domain.NodeStatus) bool { return s == domain.NodeActive }

// AllEvicted 全池摘除判定: 非 evicted 的节点数为 0 且有节点。
// 供"全灭保最后池 + ALERT + /healthz degraded"(4.2)使用。
func AllEvicted(statuses []domain.NodeStatus) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, s := range statuses {
		if s == domain.NodeActive {
			return false
		}
	}
	return true
}
