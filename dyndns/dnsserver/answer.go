package dnsserver

import (
	"fmt"
	"net"
	"time"

	"github.com/miekg/dns"
)

// IPAnswer creates an A or AAAA entry dns answer.
// Returns error if ip is not a valid ipv4 or ipv6.
func IPAnswer(domainName string, ip net.IP, ttl int) ([]dns.RR, error) {
	rrType := ipType(ip.String())
	if rrType == dns.TypeNone {
		return nil, fmt.Errorf("ip is not a valid ipv4 or ipv6")
	}

	rrHeader := dns.RR_Header{
		Name:   domainName,
		Rrtype: rrType,
		Class:  dns.ClassINET,
		Ttl:    uint32(ttl),
	}

	if rrType == dns.TypeAAAA {
		return []dns.RR{&dns.AAAA{Hdr: rrHeader, AAAA: ip}}, nil
	}

	return []dns.RR{&dns.A{Hdr: rrHeader, A: ip}}, nil
}

// CNameAnswer creates a CNAME entry dns answer.
func CNameAnswer(domainName string, target string, ttl int) []dns.RR {
	rrHeader := dns.RR_Header{
		Name:   domainName,
		Rrtype: dns.TypeCNAME,
		Class:  dns.ClassINET,
		Ttl:    uint32(ttl),
	}

	return []dns.RR{&dns.CNAME{Hdr: rrHeader, Target: target}}
}

// SOAAnswer creates a SOA entry dns answer.
func SOAAnswer(domainName string, nameServer string, ttl int) []dns.RR {
	rrHeader := dns.RR_Header{
		Name:   domainName,
		Rrtype: dns.TypeSOA,
		Class:  dns.ClassINET,
		Ttl:    uint32(ttl),
	}

	return []dns.RR{&dns.SOA{
		Hdr:     rrHeader,
		Serial:  uint32(time.Now().Unix()),
		Refresh: uint32(3600),
		Retry:   uint32(900),
		Expire:  uint32(604800),
		Minttl:  uint32(86400),
		Ns:      nameServer,
		Mbox:    "root." + domainName,
	}}
}
