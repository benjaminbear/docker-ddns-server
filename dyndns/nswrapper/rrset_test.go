package nswrapper

import (
	"strings"
	"testing"
)

func TestRDataValidation(t *testing.T) {
	for _, tc := range []struct {
		k, v string
		ok   bool
	}{
		{"A", "192.0.2.1", true}, {"AAAA", "2001:db8::1", true}, {"AAAA", "192.0.2.1", false}, {"MX", "10 mail.example.test", true}, {"MX", "0 .", true}, {"MX", "65536 mail.example.test", false}, {"MX", "10 192.0.2.1", false}, {"SRV", "0 5 443 service.example.test", true}, {"CAA", "0 issue letsencrypt.org", true}, {"CAA", "1 issue bad", false}, {"NS", "ns.example.test", true}, {"CNAME", "evil\nsend", false}, {"BOGUS", "x", false},
	} {
		_, e := FormatRData(tc.k, tc.v)
		if (e == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, e)
		}
	}
	text := strings.Repeat("x", 256) + "\"\\\n"
	formatted, e := FormatRData("TXT", text)
	if e != nil || strings.Contains(formatted, "\n") || !strings.Contains(formatted, "\\010") || !strings.Contains(formatted, "\" \"") {
		t.Fatalf("unsafe TXT %q %v", formatted, e)
	}
	for _, set := range []RRset{{Zone: "example.test", Name: "@", Type: "NS", TTL: 60}, {Zone: "example.test\nsend", Name: "x", Type: "TXT", TTL: 60}, {Zone: "example.test", Name: "@", Type: "CNAME", TTL: 60, Values: []string{"mail.example.test"}}, {Zone: "example.test", Name: "bad", Type: "SRV", TTL: 60, Values: []string{"0 0 443 mail.example.test"}}} {
		if _, e := RRScript([]RRset{set}); e == nil {
			t.Fatal("accepted invalid RRset")
		}
	}
	script, e := RRScript([]RRset{{Zone: "example.test", Name: "@", Type: "TXT", TTL: 60, Values: []string{"first", "second"}}})
	if e != nil || strings.Count(script, "update add") != 2 || strings.Count(script, "send\n") != 1 {
		t.Fatal(script, e)
	}
}
