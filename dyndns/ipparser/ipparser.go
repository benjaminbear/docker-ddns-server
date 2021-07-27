package ipparser

import (
	"net"
)

// ValidIP4 tells you if a given string is a valid IPv4 address.
func ValidIP4(ipAddress string) bool {
	testInput := net.ParseIP(ipAddress)
	if testInput == nil {
		return false
	}

	return testInput.To4() != nil
}

// ValidIP6 tells you if a given string is a valid IPv6 address.
func ValidIP6(ip6Address string) bool {
	testInputIP6 := net.ParseIP(ip6Address)
	if testInputIP6 == nil {
		return false
	}

	return testInputIP6.To16() != nil
}
