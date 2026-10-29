package nswrapper

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// TrustedProxies is an explicit allowlist; an empty list trusts no proxy headers.
type TrustedProxies []*net.IPNet

func ParseTrustedProxies(value string) (TrustedProxies, error) {
	var proxies TrustedProxies
	if strings.TrimSpace(value) == "" {
		return proxies, nil
	}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			proxies = append(proxies, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		} else {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid DDNS_TRUSTED_PROXIES entry %q", entry)
			}
			proxies = append(proxies, network)
		}
	}
	return proxies, nil
}

func remoteIP(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return address
}

func (p TrustedProxies) TrustsRemote(address string) bool {
	ip := net.ParseIP(remoteIP(address))
	for _, network := range p {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP walks XFF from the trusted peer toward the first untrusted hop.
// Malformed chains fail closed to the socket peer. X-Real-IP is a fallback
// only when XFF is absent and the peer is explicitly trusted.
func (p TrustedProxies) ClientIP(r *http.Request) string {
	peer := remoteIP(r.RemoteAddr)
	if !p.TrustsRemote(peer) {
		return peer
	}
	if chain := r.Header.Get("X-Forwarded-For"); chain != "" {
		hops := strings.Split(chain, ",")
		current := peer
		for i := len(hops) - 1; i >= 0; i-- {
			if !p.TrustsRemote(current) {
				return current
			}
			ip := net.ParseIP(strings.TrimSpace(hops[i]))
			if ip == nil {
				return peer
			}
			current = ip.String()
		}
		return current
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return peer
}
