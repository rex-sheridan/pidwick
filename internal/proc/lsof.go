package proc

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OpenFile struct {
	FD   string
	Type string // REG, DIR, CHR, PIPE, unix, ...
	Name string
}

type Conn struct {
	Proto  string // TCP / UDP
	Family string // IPv4 / IPv6
	Local  string
	Remote string
	State  string
	Host   string // reverse-DNS name of remote, if resolved
	Scope  string // loopback, LAN, public, any
}

type Handles struct {
	Cwd       string
	Files     []OpenFile // regular open files and dirs
	Libs      []string   // mapped executables/libraries (txt/mem)
	Devices   []OpenFile
	Pipes     int
	Sockets   int // unix domain sockets
	Conns     []Conn
	Denied    bool
	Truncated bool
}

// parseLsof parses `lsof -F ftPnT` output.
func parseLsof(out string) Handles {
	var h Handles
	var fd, typ, proto, name, state string
	seenLib := map[string]bool{}
	flush := func() {
		defer func() { fd, typ, proto, name, state = "", "", "", "", "" }()
		if fd == "" {
			return
		}
		switch typ {
		case "IPv4", "IPv6":
			c := Conn{Proto: proto, Family: typ, State: state}
			if l, r, ok := strings.Cut(name, "->"); ok {
				c.Local, c.Remote = l, r
			} else {
				c.Local = name
			}
			c.Scope = scopeOf(c.Remote, c.Local)
			h.Conns = append(h.Conns, c)
		case "unix":
			h.Sockets++
		case "PIPE", "FIFO":
			h.Pipes++
		case "CHR":
			h.Devices = append(h.Devices, OpenFile{fd, typ, name})
		default:
			switch fd {
			case "cwd":
				h.Cwd = name
			case "txt", "mem":
				if !seenLib[name] {
					seenLib[name] = true
					h.Libs = append(h.Libs, name)
				}
			case "rtd":
			default:
				h.Files = append(h.Files, OpenFile{fd, typ, name})
			}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
		case 'f':
			flush()
			fd = v
		case 't':
			typ = v
		case 'P':
			proto = v
		case 'n':
			name = v
		case 'T':
			if s, ok := strings.CutPrefix(v, "ST="); ok {
				state = s
			}
		}
	}
	flush()
	return h
}

func splitHostPort(s string) (string, string) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, ""
	}
	return strings.Trim(s[:i], "[]"), s[i+1:]
}

func scopeOf(remote, local string) string {
	addr := remote
	if addr == "" {
		addr = local
	}
	host, _ := splitHostPort(addr)
	if host == "*" || host == "" {
		return "any"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "public"
	}
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsMulticast():
		return "LAN"
	}
	return "public"
}

// inspectHandles lists the open files and network connections for pid.
func inspectHandles(ctx context.Context, pid int, resolve bool) Handles {
	out, err := run(ctx, 8*time.Second, "lsof", "-nP", "-p", strconv.Itoa(pid), "-F", "ftPnT")
	h := parseLsof(out)
	if err != nil && len(h.Files)+len(h.Libs)+len(h.Conns) == 0 && h.Cwd == "" {
		h.Denied = true
		return h
	}
	if resolve {
		resolveHosts(ctx, h.Conns)
	}
	sort.SliceStable(h.Conns, func(i, j int) bool {
		a, b := h.Conns[i], h.Conns[j]
		if (a.State == "LISTEN") != (b.State == "LISTEN") {
			return a.State == "LISTEN"
		}
		return a.Remote < b.Remote
	})
	return h
}

func resolveHosts(ctx context.Context, conns []Conn) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cache := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range conns {
		c := &conns[i]
		if c.Remote == "" || c.Scope != "public" {
			continue
		}
		host, _ := splitHostPort(c.Remote)
		mu.Lock()
		_, seen := cache[host]
		if !seen {
			cache[host] = ""
		}
		mu.Unlock()
		if seen {
			continue
		}
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			names, err := net.DefaultResolver.LookupAddr(ctx, host)
			if err == nil && len(names) > 0 {
				mu.Lock()
				cache[host] = strings.TrimSuffix(names[0], ".")
				mu.Unlock()
			}
		}(host)
	}
	wg.Wait()
	for i := range conns {
		if conns[i].Remote != "" {
			host, _ := splitHostPort(conns[i].Remote)
			conns[i].Host = cache[host]
		}
	}
}
