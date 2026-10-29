package nswrapper

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/benjaminbear/docker-ddns-server/dyndns/ipparser"
)

// GetIPType finds out if the IP is IPv4 or IPv6
func GetIPType(ipAddr string) string {
	if ipparser.ValidIP4(ipAddr) {
		return "A"
	} else if ipparser.ValidIP6(ipAddr) {
		return "AAAA"
	} else {
		return ""
	}
}

// GetCallerIP returns the client address using only configured trusted proxies.
func GetCallerIP(r *http.Request, proxies TrustedProxies) (string, error) {
	ip := proxies.ClientIP(r)
	if net.ParseIP(ip) == nil {
		return "", errors.New("invalid client address")
	}
	return ip, nil
}

// ShrinkUserAgent simply cuts the user agent information if its too long to display.
func ShrinkUserAgent(agent string) string {
	agentParts := strings.Split(agent, " ")

	return agentParts[0]
}

// ipRange - a structure that holds the start and end of a range of ip addresses
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
