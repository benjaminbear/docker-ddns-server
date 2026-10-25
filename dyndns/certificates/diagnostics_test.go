package certificates

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthoritativeDefaultAndOverride(t *testing.T) {
	t.Setenv("DDNS_ACME_AUTHORITATIVE_NS", "")
	if authoritativeNameserver() != "127.0.0.1:53" {
		t.Fatal("default must use colocated BIND")
	}
	t.Setenv("DDNS_ACME_AUTHORITATIVE_NS", " 192.0.2.1:5353 ")
	if authoritativeNameserver() != "192.0.2.1:5353" {
		t.Fatal("explicit override ignored")
	}
}
func TestCertificateFailureRedactsProviderPayload(t *testing.T) {
	for _, detail := range []string{"propagation: timeout token=secret", "account failure token=secret"} {
		message := certificateFailureMessage(errors.New(detail))
		if strings.Contains(message, "secret") {
			t.Fatal("provider payload exposed")
		}
	}
	if !strings.Contains(certificateFailureMessage(errors.New("propagation: timeout")), "readiness") {
		t.Fatal("missing actionable diagnosis")
	}
}
