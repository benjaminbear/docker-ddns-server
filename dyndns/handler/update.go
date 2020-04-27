package handler

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"github.com/benjaminbear/docker-ddns-server/dyndns/ipparser"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

func (h *Handler) updateRecord(hostname string, ipAddr string, addrType string, zone string, ttl int) error {
	fmt.Printf("%s record update request: %s -> %s\n", addrType, hostname, ipAddr)

	f, err := ioutil.TempFile(os.TempDir(), "dyndns")
	if err != nil {
		return err
	}

	defer os.Remove(f.Name())
	w := bufio.NewWriter(f)

	w.WriteString(fmt.Sprintf("server %s\n", "localhost"))
	w.WriteString(fmt.Sprintf("zone %s\n", zone))
	w.WriteString(fmt.Sprintf("update delete %s.%s %s\n", hostname, zone, addrType))
	w.WriteString(fmt.Sprintf("update add %s.%s %v %s %s\n", hostname, zone, ttl, addrType, ipAddr))
	w.WriteString("send\n")

	w.Flush()
	f.Close()

	cmd := exec.Command("/usr/bin/nsupdate", f.Name())
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("%v: %v", err, stderr.String())
	}

	if out.String() != "" {
		return fmt.Errorf(out.String())
	}

	return nil
}

func (h *Handler) deleteRecord(hostname string, zone string) error {
	fmt.Printf("record delete request: %s\n", hostname)

	f, err := ioutil.TempFile(os.TempDir(), "dyndns")
	if err != nil {
		return err
	}

	defer os.Remove(f.Name())
	w := bufio.NewWriter(f)

	w.WriteString(fmt.Sprintf("server %s\n", "localhost"))
	w.WriteString(fmt.Sprintf("zone %s\n", zone))
	w.WriteString(fmt.Sprintf("update delete %s.%s\n", hostname, zone))
	w.WriteString("send\n")

	w.Flush()
	f.Close()

	cmd := exec.Command("/usr/bin/nsupdate", f.Name())
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("%v: %v", err, stderr.String())
	}

	if out.String() != "" {
		return fmt.Errorf(out.String())
	}

	return nil
}

func getIPType(ipAddr string) string {
	if ipparser.ValidIP4(ipAddr) {
		return "A"
	} else if ipparser.ValidIP6(ipAddr) {
		return "AAAA"
	} else {
		return ""
	}
}

func getCallerIP(r *http.Request) (string, error) {
	fmt.Println("request", r.Header)
	for _, h := range []string{"X-Real-Ip", "X-Forwarded-For"} {
		addresses := strings.Split(r.Header.Get(h), ",")
		// march from right to left until we get a public address
		// that will be the address right before our proxy.
		for i := len(addresses) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(addresses[i])
			// header can contain spaces too, strip those out.
			realIP := net.ParseIP(ip)
			if !realIP.IsGlobalUnicast() || isPrivateSubnet(realIP) {
				// bad address, go to next
				continue
			}
			return ip, nil
		}
	}
	return "", errors.New("no match")
}

//ipRange - a structure that holds the start and end of a range of ip addresses
type ipRange struct {
	start net.IP
	end   net.IP
}

// inRange - check to see if a given ip address is within a range given
func inRange(r ipRange, ipAddress net.IP) bool {
	// strcmp type byte comparison
	if bytes.Compare(ipAddress, r.start) >= 0 && bytes.Compare(ipAddress, r.end) < 0 {
		return true
	}
	return false
}

var privateRanges = []ipRange{
	ipRange{
		start: net.ParseIP("10.0.0.0"),
		end:   net.ParseIP("10.255.255.255"),
	},
	ipRange{
		start: net.ParseIP("100.64.0.0"),
		end:   net.ParseIP("100.127.255.255"),
	},
	ipRange{
		start: net.ParseIP("172.16.0.0"),
		end:   net.ParseIP("172.31.255.255"),
	},
	ipRange{
		start: net.ParseIP("192.0.0.0"),
		end:   net.ParseIP("192.0.0.255"),
	},
	ipRange{
		start: net.ParseIP("192.168.0.0"),
		end:   net.ParseIP("192.168.255.255"),
	},
	ipRange{
		start: net.ParseIP("198.18.0.0"),
		end:   net.ParseIP("198.19.255.255"),
	},
}

// isPrivateSubnet - check to see if this ip is in a private subnet
func isPrivateSubnet(ipAddress net.IP) bool {
	// my use case is only concerned with ipv4 atm
	if ipCheck := ipAddress.To4(); ipCheck != nil {
		// iterate over all our ranges
		for _, r := range privateRanges {
			// check if this ip is in a private range
			if inRange(r, ipAddress) {
				return true
			}
		}
	}
	return false
}

func shrinkUserAgent(agent string) string {
	agentParts := strings.Split(agent, " ")

	return agentParts[0]
}
