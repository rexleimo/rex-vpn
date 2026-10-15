package marzban

import (
	"context"
	"testing"
)

// decodeSubToken 用 work-6.2 真实 Marzban 生成的 token 验证(username=testuser1)。
func TestDecodeSubTokenReal(t *testing.T) {
	// 真实订阅 URL 尾部 token(由 Marzban create_subscription_token 生成)
	tok := "dGVzdHVzZXIxLDE3OTIwNjk5NDk3ds1eoM3Db"
	got, err := decodeSubToken(tok)
	if err != nil {
		t.Fatalf("decodeSubToken: %v", err)
	}
	if got != "testuser1" {
		t.Fatalf("username = %q, want testuser1", got)
	}
}

func TestDecodeSubTokenBad(t *testing.T) {
	if _, err := decodeSubToken("short"); err == nil {
		t.Fatal("short token should error")
	}
}

// 禁用桩行为(wc-5 前置: Marzban 未配置不 panic)。
func TestDisabledStub(t *testing.T) {
	d := Disabled()
	if _, err := d.GetUserByToken(context.Background(), "x"); err != ErrNotFound {
		t.Fatalf("disabled GetUserByToken err = %v, want ErrNotFound", err)
	}
	if _, err := d.CreateInvoice(context.Background(), "x", "p"); err != ErrNotSupported {
		t.Fatalf("disabled CreateInvoice err = %v, want ErrNotSupported", err)
	}
	if invs, _ := d.ListInvoices(context.Background(), "x"); invs != nil {
		t.Fatalf("disabled ListInvoices = %v, want nil", invs)
	}
}
