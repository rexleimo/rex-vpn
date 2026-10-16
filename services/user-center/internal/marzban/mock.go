// Package marzban 的 mock 实现: 单测/联调前不依赖真实 Marzban(wc-1.3 验收)。
package marzban

import "context"

// Mock 是 Client 的内存实现, 可预置用户/订阅/发票; provision 调用被记录。
type Mock struct {
	Users    map[string]User
	Subs     map[string]string // token -> sub 内容
	Invoices map[string][]Invoice
	ByPlan   map[string]func(plan string) Invoice
	Created  []Created
	Mods     map[string]UserMod
}

// NewMock 构造一个带默认数据的 Mock。
func NewMock() *Mock {
	m := &Mock{
		Users: map[string]User{
			"test-token": {
				ID: 1, Username: "tester", Status: "active",
				ExpireDays: 30, LimitTraffic: 50 << 30,
				UsedTraffic: 10 << 30, Online: true,
			},
		},
		Subs: map[string]string{
			"test-token": "proxies:\n  - name: n1\n    type: vless\n",
		},
		Invoices: map[string][]Invoice{},
		Mods:     map[string]UserMod{},
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

func (m *Mock) SubContent(ctx context.Context, token, userAgent string) (string, error) {
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

// CreateUser 记录 provision 并生成可回读的 mock token(mocktok-<user>, 不满足 decodeSubToken, 但 dashboard 直接持 token 使用)。
func (m *Mock) CreateUser(ctx context.Context, spec UserSpec) (Created, error) {
	tok := "mocktok-" + spec.Username
	cr := Created{Username: spec.Username, Token: tok}
	m.Created = append(m.Created, cr)
	m.Users[tok] = User{ID: int64(len(m.Users) + 1), Username: spec.Username, Status: "active",
		ExpireDays: int(spec.Expire), LimitTraffic: spec.DataLimit}
	if _, ok := m.Subs[tok]; !ok {
		m.Subs[tok] = "proxies:\n  - name: n1\n    type: vless\n"
	}
	return cr, nil
}

// ModifyUser 按 username 更新 mock 用户并记录。
func (m *Mock) ModifyUser(ctx context.Context, username string, mod UserMod) error {
	found := false
	for tok, u := range m.Users {
		if u.Username != username {
			continue
		}
		found = true
		if mod.Expire != nil {
			u.ExpireDays = int(*mod.Expire)
		}
		if mod.DataLimit != nil {
			u.LimitTraffic = *mod.DataLimit
		}
		if mod.UsedTraffic != nil {
			u.UsedTraffic = *mod.UsedTraffic
		}
		if mod.Status != "" {
			u.Status = mod.Status
		}
		m.Users[tok] = u
	}
	if !found {
		return ErrNotFound
	}
	m.Mods[username] = mod
	return nil
}
