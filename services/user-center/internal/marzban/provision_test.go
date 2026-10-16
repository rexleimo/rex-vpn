package marzban

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recorded struct {
	method, path string
	body         map[string]any
}

func provisionServer(t *testing.T, reply func(rec recorded, n int) (int, string)) (*httptest.Server, *[]recorded) {
	t.Helper()
	var recs []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok123"})
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		rec := recorded{r.Method, r.URL.Path, body}
		recs = append(recs, rec)
		code, payload := reply(rec, len(recs))
		w.WriteHeader(code)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &recs
}

func testClient(t *testing.T, srv *httptest.Server) Client {
	t.Setenv("MARZBAN_API_BASE", srv.URL)
	t.Setenv("MARZBAN_ADMIN_USER", "adm")
	t.Setenv("MARZBAN_ADMIN_PASSWORD", "pw")
	c := NewHTTP()
	if c == nil {
		t.Fatal("nil client")
	}
	return c
}

func TestCreateUserSuccess(t *testing.T) {
	t.Setenv("MARZBAN_INBOUND_IDS", "vmess-1, vless-2")
	srv, recs := provisionServer(t, func(rec recorded, n int) (int, string) {
		return 200, `{"username":"uc-abc12345","token":"TOKEN_X"}`
	})
	c := testClient(t, srv)
	cr, err := c.CreateUser(context.Background(), UserSpec{Username: "uc-abc12345", Expire: 1800000000, DataLimit: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if cr.Token != "TOKEN_X" {
		t.Fatalf("token %q", cr.Token)
	}
	rec := (*recs)[0]
	if rec.method != "POST" || rec.path != "/api/users" {
		t.Fatalf("%s %s", rec.method, rec.path)
	}
	if rec.body["username"] != "uc-abc12345" || rec.body["status"] != "active" {
		t.Fatalf("body %+v", rec.body)
	}
	if rec.body["expire"].(float64) != 1800000000 {
		t.Fatalf("expire %+v", rec.body["expire"])
	}
	ids, ok := rec.body["inbounds"].([]any)
	if !ok || len(ids) != 2 || ids[0] != "vmess-1" {
		t.Fatalf("inbounds %+v", rec.body["inbounds"])
	}
}

func TestCreateUserConflictFallsBackToRead(t *testing.T) {
	srv, recs := provisionServer(t, func(rec recorded, n int) (int, string) {
		if rec.method == "POST" {
			return 409, `{}`
		}
		return 200, `{"username":"uc-abc12345","token":"TOKEN_OLD"}`
	})
	c := testClient(t, srv)
	cr, err := c.CreateUser(context.Background(), UserSpec{Username: "uc-abc12345"})
	if err != nil {
		t.Fatal(err)
	}
	if cr.Token != "TOKEN_OLD" {
		t.Fatalf("fallback token %q", cr.Token)
	}
	if (*recs)[1].method != "GET" {
		t.Fatalf("second call %s", (*recs)[1].method)
	}
}

func TestCreateUserEmptyTokenIsError(t *testing.T) {
	srv, _ := provisionServer(t, func(rec recorded, n int) (int, string) {
		return 200, `{"username":"uc-x"}`
	})
	c := testClient(t, srv)
	if _, err := c.CreateUser(context.Background(), UserSpec{Username: "uc-x"}); err == nil {
		t.Fatal("must fail without token")
	}
}

func TestModifyUserPayload(t *testing.T) {
	srv, recs := provisionServer(t, func(rec recorded, n int) (int, string) {
		return 200, `{"username":"uc-x"}`
	})
	c := testClient(t, srv)
	exp := int64(1900000000)
	zero := int64(0)
	if err := c.ModifyUser(context.Background(), "uc-x", UserMod{Expire: &exp, UsedTraffic: &zero}); err != nil {
		t.Fatal(err)
	}
	rec := (*recs)[0]
	if rec.method != "PUT" || rec.path != "/api/users/uc-x" {
		t.Fatalf("%s %s", rec.method, rec.path)
	}
	if rec.body["expire"].(float64) != 1900000000 || rec.body["used_traffic"].(float64) != 0 {
		t.Fatalf("body %+v", rec.body)
	}
	if _, ok := rec.body["data_limit"]; ok {
		t.Fatal("nil fields must not be sent")
	}
}

func TestModifyUserNotFound(t *testing.T) {
	srv, _ := provisionServer(t, func(rec recorded, n int) (int, string) { return 404, `{}` })
	c := testClient(t, srv)
	if err := c.ModifyUser(context.Background(), "ghost", UserMod{}); err != ErrNotFound {
		t.Fatalf("err %v", err)
	}
}

func TestMockProvisionRoundTrip(t *testing.T) {
	m := NewMock()
	cr, err := m.CreateUser(context.Background(), UserSpec{Username: "uc-1", Expire: 123, DataLimit: 456})
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.GetUserByToken(context.Background(), cr.Token)
	if err != nil || u.Username != "uc-1" || u.LimitTraffic != 456 {
		t.Fatalf("round trip %+v %v", u, err)
	}
	exp := int64(999)
	if err := m.ModifyUser(context.Background(), "uc-1", UserMod{Expire: &exp}); err != nil {
		t.Fatal(err)
	}
	if m.Mods["uc-1"].Expire == nil || *m.Mods["uc-1"].Expire != 999 {
		t.Fatal("mod not recorded")
	}
	if err := m.ModifyUser(context.Background(), "nope", UserMod{}); err != ErrNotFound {
		t.Fatal("unknown username must ErrNotFound")
	}
}
