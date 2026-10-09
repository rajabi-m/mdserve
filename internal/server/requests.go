package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// GuardRequests binds the HTTP policy to the actual listener, including a port
// chosen by free-port fallback. Additional hosts are exact DNS names or IPs,
// never wildcard patterns; they are also restricted to the listener's port.
func GuardRequests(h http.Handler, addr net.Addr, hosts []string) (http.Handler, error) {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp.Port < 1 {
		return nil, fmt.Errorf("invalid HTTP listener address")
	}
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if !tcp.IP.IsUnspecified() {
		allowed[tcp.IP.String()] = true
	}
	for _, host := range hosts {
		host = strings.ToLower(host)
		if !validHost(host) {
			return nil, fmt.Errorf("invalid allowed host %q (use a hostname or IP without a port)", host)
		}
		allowed[host] = true
	}
	port := strconv.Itoa(tcp.Port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w = headWriter{w}
		}
		host, p, err := net.SplitHostPort(r.Host)
		if err != nil && tcp.Port == 80 && validHost(r.Host) {
			host, p, err = r.Host, "80", nil
		}
		if err != nil || p != port || !allowed[strings.ToLower(host)] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		origins, present := r.Header["Origin"]
		if present {
			if len(origins) != 1 {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			u, err := url.Parse(origins[0])
			if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || !strings.EqualFold(u.Host, r.Host) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		h.ServeHTTP(w, r)
	}), nil
}

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

type headWriter struct{ http.ResponseWriter }

func (w headWriter) Write(p []byte) (int, error) { return len(p), nil }
