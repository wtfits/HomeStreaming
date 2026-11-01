package netutil

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPAndClass(t *testing.T) {
	r := NewResolver([]string{"127.0.0.1"})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.0.16:5000"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := r.ClientIP(req); got.String() != "192.168.0.16" {
		t.Fatalf("untrusted proxy must be ignored: %s", got)
	}
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "100.101.102.103, 127.0.0.1")
	if got := r.ClientIP(req); got.String() != "100.101.102.103" {
		t.Fatalf("trusted proxy: %s", got)
	}

	cases := []struct {
		ip      string
		tailLAN bool
		want    string
	}{
		{"192.168.0.16", false, "lan"}, {"10.0.0.2", false, "lan"}, {"127.0.0.1", false, "lan"},
		{"fe80::1", false, "lan"}, {"fd00::5", false, "lan"},
		{"100.101.102.103", false, "remote"}, {"100.101.102.103", true, "lan"},
		{"24.133.1.2", false, "remote"},
	}
	for _, c := range cases {
		if got := Class(netip.MustParseAddr(c.ip), c.tailLAN); got != c.want {
			t.Errorf("%s tailnetIsLan=%v → %s, want %s", c.ip, c.tailLAN, got, c.want)
		}
	}
}
