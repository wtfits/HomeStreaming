// Package netutil resolves client IPs (honouring trusted proxies) and
// classifies connections as lan or remote.
package netutil

import (
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
)

// Resolver resolves the real client IP of a request.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver parses trusted proxy entries (IPs or CIDRs).
func NewResolver(trusted []string) *Resolver {
	r := &Resolver{}
	for _, t := range trusted {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if p, err := netip.ParsePrefix(t); err == nil {
			r.trusted = append(r.trusted, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(t); err == nil {
			r.trusted = append(r.trusted, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return r
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ClientIP returns the client address: RemoteAddr, or, when RemoteAddr is a
// trusted proxy, the last untrusted hop of X-Forwarded-For.
func (r *Resolver) ClientIP(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	addr = addr.Unmap()
	if !r.isTrusted(addr) {
		return addr
	}
	xff := req.Header.Values("X-Forwarded-For")
	var hops []string
	for _, v := range xff {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			continue
		}
		a = a.Unmap()
		if !r.isTrusted(a) {
			return a
		}
	}
	return addr
}

// IsHTTPS reports whether the request reached us over TLS (directly or via a
// trusted proxy that says so).
func (r *Resolver) IsHTTPS(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return false
	}
	if a, err := netip.ParseAddr(host); err == nil && r.isTrusted(a.Unmap()) {
		return strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

var (
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
	ula   = netip.MustParsePrefix("fc00::/7")
)

// Class returns "lan" or "remote" for a client address.
func Class(a netip.Addr, tailnetIsLAN bool) string {
	if !a.IsValid() {
		return "remote"
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback(), a.IsLinkLocalUnicast(), a.IsPrivate(), ula.Contains(a):
		return "lan"
	case tailnetIsLAN && cgnat.Contains(a):
		return "lan"
	}
	return "remote"
}

// IsTailnet reports whether a is in Tailscale's 100.64.0.0/10 range.
func IsTailnet(a netip.Addr) bool { return cgnat.Contains(a.Unmap()) }

// LocalAddresses lists LAN IPv4 and tailnet addresses of this machine.
func LocalAddresses() (lan, tailnet []string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			if !ip.Is4() {
				continue
			}
			switch {
			case cgnat.Contains(ip):
				tailnet = append(tailnet, ip.String())
			case ip.IsPrivate():
				lan = append(lan, ip.String())
			}
		}
	}
	sort.Strings(lan)
	sort.Strings(tailnet)
	return lan, tailnet
}
