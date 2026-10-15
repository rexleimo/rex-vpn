// Package marzban 的 mock 实现: 单测/联调前不依赖真实 Marzban(wc-1.3 验收)。
package marzban

import "context"

// Mock 是 Client 的内存实现, 可预置用户/订阅/发票。
type Mock struct {
	Users    map[string]User
	Subs     map[string]string // token -> sub 内容
	Invoices map[string][]Invoice
	ByPlan   map[string]func(plan string) Invoice
}

// NewMock 构造一个带默认数据的 Mock。
func NewMock() *Mock {
	m := &Mock{
		Users: map[string]User{
			"test-token": {
				ID: 1, Username: "tester", Status: "active",
				ExpireTime: 1_800_000_000, LimitTraffic: 50 << 30,
				UsedTraffic: 10 << 30, Online: true,
			},
		},
		Subs: map[string]string{
			"test-token": "proxies:\n  - name: n1\n    type: vless\n",
		},
		Invoices: map[string][]Invoice{},
	}
	return m
}

func (m *Mock) GetUserByToken(ctx context.Context, token string) (User, error) {
	u, ok := m.Users[token]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Mock) SubURL(token string) string {
	return "https://marzban.example/sub/" + token
}

func (m *Mock) SubContent(ctx context.Context, token string) (string, error) {
	if s, ok := m.Subs[token]; ok {
		return s, nil
	}
	return "", ErrNotFound
}

func (m *Mock) CreateInvoice(ctx context.Context, token, plan string) (Invoice, error) {
	if f := m.ByPlan[plan]; f != nil {
		return f(plan), nil
	}
	inv := Invoice{ID: int64(len(m.Invoices[token]) + 1), PlanName: plan, Amount: 990, Status: "pending", CreatedAt: 1_700_000_000}
	m.Invoices[token] = append(m.Invoices[token], inv)
	return inv, nil
}

func (m *Mock) ListInvoices(ctx context.Context, token string) ([]Invoice, error) {
	return m.Invoices[token], nil
}
