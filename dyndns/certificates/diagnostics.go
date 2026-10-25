package certificates

import (
	"os"
	"strings"
)

// BIND runs in this container. Avoid relying on public-address NAT reflection
// for the local readiness check; the CA still performs its own public validation.
func authoritativeNameserver() string {
	if ns := strings.TrimSpace(os.Getenv("DDNS_ACME_AUTHORITATIVE_NS")); ns != "" {
		return ns
	}
	return "127.0.0.1:53"
}

func certificateFailureMessage(err error) string {
	if strings.Contains(strings.ToLower(err.Error()), "propagation") {
		return "DNS challenge readiness check timed out. Check the local authoritative BIND server and DDNS_ACME_AUTHORITATIVE_NS; the CA must also be able to reach the zone's public nameservers."
	}
	return "Certificate operation failed. Check DNS delegation, CAA policy, CA availability and account email; then retry."
}
