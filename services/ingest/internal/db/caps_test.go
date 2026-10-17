package db

import (
	"context"
	"path/filepath"
	"testing"
)

// 能力标/证书指纹必须可写可读(xraygen 与 prober 之间的契约)。
func TestNodeCapsRoundtrip(t *testing.T) {
	s, err := NewStore("sqlite", filepath.Join(t.TempDir(), "caps.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	row := &NodeRow{Fingerprint: "fp-caps-1", Address: "1.2.3.4", Port: 443, Protocol: "trojan"}
	if err := s.UpsertRow(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCertPin(ctx, "fp-caps-1", "deadbeef"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClaudeOK(ctx, "fp-caps-1", true); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListNodes(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CertPin != "deadbeef" || !rows[0].ClaudeOK {
		t.Fatalf("roundtrip = %+v", rows)
	}
}
