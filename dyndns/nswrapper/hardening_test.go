package nswrapper

import (
	"bytes"
	"github.com/labstack/gommon/log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyTrust(t *testing.T) {
	proxies, err := ParseTrustedProxies("10.0.0.0/24,2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ peer, xff, real, want string }{
		{"192.0.2.1:80", "203.0.113.1", "203.0.113.2", "192.0.2.1"},
		{"10.0.0.1:80", "203.0.113.1, 10.0.0.2", "", "203.0.113.1"},
		{"10.0.0.1:80", "203.0.113.1, 192.0.2.2", "203.0.113.3", "192.0.2.2"},
		{"10.0.0.1:80", "garbage", "203.0.113.3", "10.0.0.1"},
		{"10.0.0.1:80", "", "203.0.113.3", "203.0.113.3"},
		{"[2001:db8::1]:80", "2001:db8::2", "", "2001:db8::2"},
		{"10.0.0.1:80", "192.168.1.2", "", "192.168.1.2"},
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = tc.peer
		req.Header.Set("X-Forwarded-For", tc.xff)
		req.Header.Set("X-Real-IP", tc.real)
		got, err := GetCallerIP(req, proxies)
		if got != tc.want || err != nil {
			t.Errorf("%+v got=%s err=%v", tc, got, err)
		}
	}
	if _, err := ParseTrustedProxies("bad-input"); err == nil {
		t.Fatal("invalid config accepted")
	}
}
func TestCallerIPDoesNotLogHeaders(t *testing.T) {
	var output bytes.Buffer
	old := log.Output()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic sensitive-test-value")
	GetCallerIP(req, nil)
	if output.Len() != 0 {
		t.Fatalf("caller-IP lookup logged request: %s", output.String())
	}
}
func TestDeleteBatch(t *testing.T) {
	script, err := deleteScript([]string{"router", "alias"}, "example.test", true)
	if err != nil {
		t.Fatal(err)
	}
	want := "server localhost\nzone example.test\n"
	for _, name := range []string{"router", "*.router", "alias", "*.alias"} {
		for _, kind := range []string{"A", "AAAA", "CNAME"} {
			want += "update delete " + name + ".example.test " + kind + "\n"
		}
	}
	want += "send\n"
	if script != want || strings.Count(script, "send\n") != 1 {
		t.Fatalf("bad batch: %s", script)
	}
	if _, err := deleteScript([]string{"alias\nsend"}, "example.test", false); err == nil {
		t.Fatal("command injection accepted")
	}
}
