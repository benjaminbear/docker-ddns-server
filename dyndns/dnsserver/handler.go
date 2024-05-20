package dnsserver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/benjaminbear/docker-ddns-server/dyndns/ipparser"

	"gorm.io/gorm"

	"github.com/benjaminbear/docker-ddns-server/dyndns/config"

	"github.com/labstack/gommon/log"

	"github.com/miekg/dns"
)

type Handler struct {
	Config *config.Config
	DB     *gorm.DB
}

func (h *Handler) Do(w dns.ResponseWriter, req *dns.Msg) {
	for _, q := range req.Question {
		log.Infof(prettyQuestion(q))

		m := new(dns.Msg)
		m.Authoritative = true
		m.SetReply(req)

		// Refuse if requested domain is not supported
		_, _, err := h.checkDomain(UnFqdn(q.Name))
		if errors.Is(err, ErrUnsupportedDomain) {
			m.Rcode = dns.RcodeRefused
			w.WriteMsg(m)
		}

		// Resolve
		answers, err := h.Resolve(q)
		if err != nil || len(answers) == 0 {
			answers, _ = h.ResolveSOA(q.Name)
			m.Ns = append(m.Ns, answers...)
			m.Rcode = dns.RcodeNameError
			w.WriteMsg(m)
			fmt.Println("return SOA as excuse with", answers, err)

			return
		}

		fmt.Println("answer to be added is", answers)
		m.Answer = append(m.Answer, answers...)

		w.WriteMsg(m)
	}
}

func (h *Handler) Resolve(question dns.Question) ([]dns.RR, error) {
	emptyAnswers := make([]dns.RR, 0)

	if question.Qclass != dns.ClassINET {
		return emptyAnswers, nil
	}

	switch question.Qtype {
	case dns.TypeA:
		fallthrough
	case dns.TypeAAAA:
		// ResolveIP
		fmt.Println("IPResolve")
		ipResolveChain := []func(fqdn string) ([]dns.RR, error){h.ResolveA, h.ResolveCName}

		for i, resolveFunc := range ipResolveChain {
			answers, err := resolveFunc(question.Name)
			fmt.Println("finished chain run", i, "with", answers, err)
			if err == nil && len(answers) > 0 {
				fmt.Println("returning", answers, err)
				return answers, err
			}
		}

		fmt.Println("finished ip w/o success")
	case dns.TypeCNAME:
		fmt.Println("CNameResolve")
		answers, err := h.ResolveCName(question.Name)

		return answers, err
	case dns.TypeTXT:
		// return TXT
		fmt.Println("TXTResolve")
	case dns.TypeSOA:
		fmt.Println("SOAResolve")
		// ignore errors, it's just empty slices
		soaAnswer, _ := h.ResolveSOA(question.Name)

		return soaAnswer, nil
	}

	fmt.Println("returning empty")
	return emptyAnswers, nil
}

var ErrUnsupportedDomain = errors.New("domain not supported")
var ErrIsDomain = errors.New("fqdn is domain")

func (h *Handler) checkDomain(fullDomainName string) (hostname string, domain string, err error) {
	for _, d := range h.Config.Domains {
		if fullDomainName == d {
			return hostname, d, ErrIsDomain
		}

		if strings.HasSuffix(fullDomainName, "."+d) && fullDomainName != "."+d {
			hostname = strings.TrimSuffix(fullDomainName, "."+d)

			return hostname, d, nil
		}
	}

	return "", "", ErrUnsupportedDomain
}

func UnFqdn(s string) string {
	if dns.IsFqdn(s) {
		return s[:len(s)-1]
	}

	return s
}

func ipType(ip string) uint16 {
	if ipparser.ValidIP4(ip) {
		return dns.TypeA
	} else if ipparser.ValidIP6(ip) {
		return dns.TypeAAAA
	}

	return dns.TypeNone
}

func prettyQuestion(question dns.Question) string {
	return fmt.Sprintf("request for %s %s %s", question.Name, dns.ClassToString[question.Qclass], dns.TypeToString[question.Qtype])
}
