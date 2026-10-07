package network

import (
	"net"
	"strings"
)

func MatchesDestination(rule, host string) bool {
	if rule == "*" || rule == host {
		return true
	}
	if strings.HasPrefix(rule, ".") && (host == rule[1:] || strings.HasSuffix(host, rule)) {
		return true
	}
	_, network, err := net.ParseCIDR(rule)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && network.Contains(ip)
}
