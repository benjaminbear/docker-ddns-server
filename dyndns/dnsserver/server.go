package dnsserver

import (
	"log"
	"net"
	"strconv"
	"time"

	"github.com/miekg/dns"
)

type Server struct {
	Host     string
	Port     int
	RTimeout time.Duration
	WTimeout time.Duration
}

func (s *Server) Addr() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

func (s *Server) Run(handler *Handler) {
	tcpHandler := dns.NewServeMux()
	tcpHandler.HandleFunc(".", handler.Do)

	udpHandler := dns.NewServeMux()
	udpHandler.HandleFunc(".", handler.Do)

	tcpServer := &dns.Server{Addr: s.Addr(),
		Net:          "tcp",
		Handler:      tcpHandler,
		ReadTimeout:  s.RTimeout,
		WriteTimeout: s.WTimeout}

	udpServer := &dns.Server{Addr: s.Addr(),
		Net:          "udp",
		Handler:      udpHandler,
		UDPSize:      65535,
		ReadTimeout:  s.RTimeout,
		WriteTimeout: s.WTimeout}

	go s.start(udpServer)
	go s.start(tcpServer)

}

func (s *Server) start(ds *dns.Server) {
	log.Printf("Start %s listener on %s\n", ds.Net, s.Addr())

	err := ds.ListenAndServe()
	if err != nil {
		log.Fatalf("Start %s listener on %s failed:%s\n", ds.Net, s.Addr(), err.Error())
	}

}
