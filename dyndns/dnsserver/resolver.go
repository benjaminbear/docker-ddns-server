package dnsserver

import (
	"errors"
	"fmt"
	"net"

	"github.com/benjaminbear/docker-ddns-server/dyndns/db"
	"github.com/miekg/dns"
)

// ResolveA checks if the requested domain is a valid A entry from the database
// and returns the corresponding A dns entry.
func (h *Handler) ResolveA(fqdn string) ([]dns.RR, error) {
	qHostname, qDomain, err := h.checkDomain(UnFqdn(fqdn))
	if errors.Is(err, ErrIsDomain) {
		answers, err := IPAnswer(fqdn, h.Config.ExternalIP, h.Config.DefaultTTL)

		return answers, err
	} else if err != nil {
		// return SOA
		return []dns.RR{}, err
	}

	hosts := new([]db.Host)
	numHosts := h.DB.Where(&db.Host{Hostname: qHostname, Domain: qDomain}).Find(hosts).RowsAffected
	if numHosts > 0 {
		host := (*hosts)[0]
		answers, err := IPAnswer(fqdn, net.ParseIP(host.Ip), host.Ttl)

		return answers, err
	}

	cnames := new([]db.CName)
	numCnames := h.DB.Joins("Target").Where(&db.CName{Hostname: qHostname}).Find(cnames, "Target.domain = ?", qDomain).RowsAffected
	if numCnames > 0 {
		cname := (*cnames)[0]
		answers := CNameAnswer(fqdn, cname.Target.FullDomain()+".", cname.Ttl)

		aAnswers, err := IPAnswer(cname.Target.FullDomain()+".", net.ParseIP(cname.Target.Ip), cname.Target.Ttl)
		if err != nil {
			// return SOA
			return []dns.RR{}, err
		}

		return append(answers, aAnswers...), nil
	}

	return []dns.RR{}, nil
}

// ResolveCName checks if the requested domain is a valid CNAME entry from the database
// and returns the corresponding CNAME and A dns entry.
func (h *Handler) ResolveCName(fqdn string) ([]dns.RR, error) {
	qHostname, qDomain, err := h.checkDomain(UnFqdn(fqdn))
	if err != nil {
		// return SOA
		return []dns.RR{}, err
	}

	cnames := new([]db.CName)
	if num := h.DB.Joins("Target").Where(&db.CName{Hostname: qHostname}).Find(cnames, "Target.domain = ?", qDomain).RowsAffected; num < 1 {

		return []dns.RR{}, nil
	}

	cname := (*cnames)[0]
	answers := CNameAnswer(fqdn, cname.Target.FullDomain()+".", cname.Ttl)

	return answers, nil
}

func (h *Handler) ResolveSOA(fqdn string) ([]dns.RR, error) {
	_, qDomain, err := h.checkDomain(UnFqdn(fqdn))
	fmt.Println(qDomain, err)
	if errors.Is(err, ErrIsDomain) {
		qDomain = UnFqdn(fqdn)
	} else if err != nil {
		return []dns.RR{}, err
	}

	for _, d := range h.Config.Domains {
		if d == qDomain {
			return SOAAnswer(d+".", h.Config.ParentNS+".", h.Config.DefaultTTL), nil
		}
	}

	return []dns.RR{}, fmt.Errorf("requesting for unsupported domain")
}
