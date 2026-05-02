// main.go
package main

import (
	_ "embed"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/favicon.png
var faviconPNG []byte

//go:embed static/openapi.json
var openapiJSON []byte

// ---------------------------------------------------------------------------
//  Request / Response structs
// ---------------------------------------------------------------------------
type DNSQueryRequest struct {
	Class      string   `json:"class"`
	Type       string   `json:"type"`
	Record     string   `json:"record"`
	DNSServers []string `json:"dnsservers,omitempty"`
	Timeout    int      `json:"timeout,omitempty"` // seconds, optional
}

type Answer struct {
	Record string `json:"record"`
	Type   string `json:"type"`
	TTL    uint32 `json:"ttl"`
	Data   string `json:"data"`
}

type DNSQueryResponse struct {
	Status  string   `json:"status"`
	Answers []Answer `json:"answers"`
}

// ---------------------------------------------------------------------------
//  Environment / constants
// ---------------------------------------------------------------------------
const (
	envDNSServers = "DNS_SERVERS"
	envListenAddr = "LISTEN_ADDR"
	dnsTimeout    = 5 * time.Second
)

// ---------------------------------------------------------------------------
//  Main entry point
// ---------------------------------------------------------------------------
func main() {
	// CLI flags take priority over environment variables, which take priority
	// over hard-coded defaults.
	flagListenAddr := flag.String("listen-addr", "", "Address and port to listen on (overrides "+envListenAddr+")")
	flagDNSServers := flag.String("dns-servers", "", "Comma-separated fallback DNS servers host:port (overrides "+envDNSServers+")")
	flag.Parse()

	addr := resolveConfig(*flagListenAddr, envListenAddr, "127.0.0.1:8080")
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	// Store the resolved DNS servers list in a package-level variable so that
	// dnsHandler can read it without re-parsing flags or env vars on every request.
	defaultDNSServers = resolveServerList(*flagDNSServers, envDNSServers)

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/favicon.png", faviconHandler)
	http.HandleFunc("/openapi.json", openapiHandler)
	http.HandleFunc("/api/v1/dns", dnsHandler)

	log.Printf("DNS-over-HTTP API listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server exited: %v", err)
	}
}

// defaultDNSServers holds the fallback server list resolved at startup from
// CLI flags and environment variables.
var defaultDNSServers []string

// resolveConfig returns the first non-empty value among: CLI flag, environment
// variable, and hard-coded fallback.
func resolveConfig(flagVal, envKey, fallback string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

// resolveServerList builds a DNS server list from a raw comma-separated string.
// It falls back to the environment variable, then to Google public DNS.
func resolveServerList(flagVal, envKey string) []string {
	raw := flagVal
	if raw == "" {
		raw = os.Getenv(envKey)
	}
	if raw != "" {
		var servers []string
		for _, s := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				servers = append(servers, trimmed)
			}
		}
		if len(servers) > 0 {
			return servers
		}
	}
	return []string{"8.8.8.8:53", "8.8.4.4:53"}
}

// ---------------------------------------------------------------------------
//  HTTP handlers
// ---------------------------------------------------------------------------
func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Write(faviconPNG)
}

func openapiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(openapiJSON)
}

func dnsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed; use POST", http.StatusMethodNotAllowed)
		return
	}

	var req DNSQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, DNSQueryResponse{Status: "ERROR"})
		return
	}

	qClass, err := dnsClassFromString(req.Class)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, DNSQueryResponse{Status: "ERROR"})
		return
	}
	qType, err := dnsTypeFromString(req.Type)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, DNSQueryResponse{Status: "ERROR"})
		return
	}

	var timeout time.Duration = dnsTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	servers := req.DNSServers
	if len(servers) == 0 {
		servers = defaultDNSServers
	}

	var fqdn string
	if req.Record == "" || req.Record == "." {
		fqdn = "."
	} else {
		fqdn = dns.Fqdn(req.Record)
	}

	// newMsg builds a fresh DNS message for each call to avoid mutations
	// across retries and across UDP/TCP protocol switches.
	newMsg := func() *dns.Msg {
		m := new(dns.Msg)
		m.SetQuestion(fqdn, qType)
		m.Question[0].Qclass = qClass
		m.SetEdns0(4096, true)
		return m
	}

	var resp *dns.Msg
	var lookupErr error

	for _, srv := range servers {
		if !strings.Contains(srv, ":") {
			srv = net.JoinHostPort(srv, "53")
		}

		// UDP attempt.
		udpClient := &dns.Client{Net: "udp", Timeout: timeout}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		r, _, err := udpClient.ExchangeContext(ctx, newMsg(), srv)
		cancel()

		if err != nil {
			lookupErr = err
			continue
		}

		// TCP fallback when the UDP response is truncated (TC bit set).
		// This is the common case for large TXT records.
		if r.Truncated {
			tcpClient := &dns.Client{Net: "tcp", Timeout: timeout}
			ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
			rTCP, _, errTCP := tcpClient.ExchangeContext(ctx2, newMsg(), srv)
			cancel2()

			if errTCP == nil {
				r = rTCP
			}
			// If TCP also fails, keep the partial UDP response.
		}

		resp = r
		break
	}

	if resp == nil {
		status := "ERROR"
		if lookupErr != nil && errors.Is(lookupErr, context.DeadlineExceeded) {
			status = "TMOUT"
		}
		writeJSON(w, http.StatusOK, DNSQueryResponse{Status: status, Answers: []Answer{}})
		return
	}

	if resp.Rcode == dns.RcodeNameError {
		writeJSON(w, http.StatusOK, DNSQueryResponse{Status: "NXDOMAIN", Answers: []Answer{}})
		return
	}

	var answers []Answer
	for _, rr := range resp.Answer {
		answers = append(answers, rrToAnswer(rr))
	}

	writeJSON(w, http.StatusOK, DNSQueryResponse{
		Status:  "SUCCESS",
		Answers: answers,
	})
}

// ---------------------------------------------------------------------------
//  Helpers
// ---------------------------------------------------------------------------
func writeJSON(w http.ResponseWriter, statusCode int, resp DNSQueryResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}

func dnsClassFromString(s string) (uint16, error) {
	switch strings.ToUpper(s) {
	case "IN":
		return dns.ClassINET, nil
	case "CH":
		return dns.ClassCHAOS, nil
	case "HS":
		return dns.ClassHESIOD, nil
	case "CS":
		return dns.ClassCSNET, nil
	default:
		return 0, fmt.Errorf("unknown class %s", s)
	}
}

func dnsTypeFromString(s string) (uint16, error) {
	switch strings.ToUpper(s) {
	case "A":
		return dns.TypeA, nil
	case "AAAA":
		return dns.TypeAAAA, nil
	case "CNAME":
		return dns.TypeCNAME, nil
	case "MX":
		return dns.TypeMX, nil
	case "NS":
		return dns.TypeNS, nil
	case "PTR":
		return dns.TypePTR, nil
	case "SOA":
		return dns.TypeSOA, nil
	case "TXT":
		return dns.TypeTXT, nil
	case "SRV":
		return dns.TypeSRV, nil
	case "NAPTR":
		return dns.TypeNAPTR, nil
	case "OPT":
		return dns.TypeOPT, nil
	case "ANY":
		return dns.TypeANY, nil
	default:
		return 0, fmt.Errorf("unknown type %s", s)
	}
}

// rrToAnswer converts any dns.RR into the Answer struct expected by the API.
func rrToAnswer(rr dns.RR) Answer {
	header := rr.Header()
	var data string

	switch r := rr.(type) {
	case *dns.A:
		data = r.A.String()
	case *dns.AAAA:
		data = r.AAAA.String()
	case *dns.CNAME:
		data = r.Target
	case *dns.MX:
		data = fmt.Sprintf("%d %s", r.Preference, r.Mx)
	case *dns.NS:
		data = r.Ns
	case *dns.PTR:
		data = r.Ptr
	case *dns.SOA:
		data = fmt.Sprintf("%s %s %d %d %d %d %d",
			r.Ns, r.Mbox, r.Serial, r.Refresh, r.Retry, r.Expire, r.Minttl)
	case *dns.TXT:
		data = strings.Join(r.Txt, " ")
	case *dns.SRV:
		data = fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, r.Target)
	case *dns.NAPTR:
		data = fmt.Sprintf("%d %d \"%s\" \"%s\" \"%s\" %s",
			r.Order, r.Preference, r.Flags, r.Service, r.Regexp, r.Replacement)
	default:
		full := rr.String()
		parts := strings.Fields(full)
		if len(parts) >= 4 {
			data = strings.Join(parts[4:], " ")
		} else {
			data = full
		}
	}

	return Answer{
		Record: strings.TrimSuffix(header.Name, "."),
		Type:   dns.TypeToString[header.Rrtype],
		TTL:    header.Ttl,
		Data:   data,
	}
}
