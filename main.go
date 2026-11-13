package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type server struct {
	ign *Client
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	var dialer Dialer
	if cfg.Direct {
		dialer = &net.Dialer{Timeout: 10 * time.Second}
		log.Printf("dial mode: direct")
	} else {
		tunnel, err := NewTunnel(cfg.JumpUser, cfg.JumpAddr, cfg.SSHKeyPath)
		if err != nil {
			log.Fatalf("ssh tunnel: %v", err)
		}
		defer tunnel.Close()
		dialer = tunnel
		log.Printf("dial mode: ssh tunnel via %s@%s", cfg.JumpUser, cfg.JumpAddr)
	}

	s := &server{ign: NewClient(cfg, dialer)}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/device/", s.handleDevice)   // /device/<ip>[:<port>]/<acn path>
	mux.HandleFunc("/clients/", s.handleClients) // /clients/<ip>?ifname=ath1
	mux.HandleFunc("/metro/", s.handleMetro)     // /metro/<ip>?ifname=wlan0&freq=60
	mux.HandleFunc("/rssi/", s.handleRSSI)       // /rssi/<ip>
	mux.HandleFunc("/cache", s.handleCache)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", s.handleRoot)

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      instrument(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("ignitenet-proxy listening on %s (device port %d)", cfg.Listen, cfg.DevicePort)
	log.Fatal(srv.ListenAndServe())
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeUsage(w)
}

// handleDevice proxies an arbitrary acn path:
// /device/<ip>[:<port>]/admin/status/connected_clients?ifname=ath1
func (s *server) handleDevice(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/device/")
	host, path, _ := strings.Cut(rest, "/")
	ip, port, err := parseTarget(host, r.URL.Query().Get("port"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Preserve the original query string (minus our own "port" param) for the device.
	if q := deviceQuery(r); q != "" {
		path += "?" + q
	}
	s.proxy(w, ip, port, path)
}

// handleClients is a convenience wrapper for connected_clients.
func (s *server) handleClients(w http.ResponseWriter, r *http.Request) {
	ip, port, err := parseTarget(strings.Trim(strings.TrimPrefix(r.URL.Path, "/clients/"), "/"), r.URL.Query().Get("port"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	ifname := r.URL.Query().Get("ifname")
	if ifname == "" {
		ifname = "ath1"
	}
	s.proxy(w, ip, port, "admin/status/connected_clients?ifname="+ifname)
}

// handleMetro is a convenience wrapper for connected_clients_metro.
func (s *server) handleMetro(w http.ResponseWriter, r *http.Request) {
	ip, port, err := parseTarget(strings.Trim(strings.TrimPrefix(r.URL.Path, "/metro/"), "/"), r.URL.Query().Get("port"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	ifname := r.URL.Query().Get("ifname")
	if ifname == "" {
		ifname = "wlan0"
	}
	freq := r.URL.Query().Get("freq")
	if freq == "" {
		freq = "60"
	}
	s.proxy(w, ip, port, "admin/status/connected_clients_metro?ifname="+ifname+"&freq="+freq)
}

// handleRSSI returns the normalized, merged signal summary.
func (s *server) handleRSSI(w http.ResponseWriter, r *http.Request) {
	ip, port, err := parseTarget(strings.Trim(strings.TrimPrefix(r.URL.Path, "/rssi/"), "/"), r.URL.Query().Get("port"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	report, status, err := s.ign.RSSI(ip, port)
	if err != nil {
		writeJSONError(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(report)
}

func (s *server) handleCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.ign.PortCache())
}

// deviceQuery returns the request's query string without our own "port" key.
func deviceQuery(r *http.Request) string {
	q := r.URL.Query()
	q.Del("port")
	return q.Encode()
}

// parseTarget splits "<ip>" or "<ip>:<port>" with an optional ?port override.
func parseTarget(host, portQuery string) (ip, port string, err error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", "", fmt.Errorf("missing device IP")
	}
	ip = host
	if h, p, splitErr := net.SplitHostPort(host); splitErr == nil {
		ip, port = h, p
	}
	if portQuery != "" {
		port = portQuery
	}
	if net.ParseIP(ip) == nil {
		return "", "", fmt.Errorf("invalid device IP: %q", ip)
	}
	if port != "" {
		if n, convErr := strconv.Atoi(port); convErr != nil || n < 1 || n > 65535 {
			return "", "", fmt.Errorf("invalid port: %q", port)
		}
	}
	return ip, port, nil
}

func (s *server) proxy(w http.ResponseWriter, ip, port, path string) {
	body, contentType, err := s.ign.Fetch(ip, port, path)
	if err != nil {
		if len(body) > 0 {
			w.Header().Set("Content-Type", orJSON(contentType))
			w.WriteHeader(http.StatusBadGateway)
			w.Write(body)
			return
		}
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", orJSON(contentType))
	w.Write(body)
}

func orJSON(ct string) string {
	if ct == "" {
		return "application/json; charset=utf-8"
	}
	return ct
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeUsage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{
		"service": "ignitenet-proxy",
		"usage": []string{
			"GET /rssi/<ip>[:<port>]                       -> normalized wifi+metro RSSI summary",
			"GET /clients/<ip>[:<port>]?ifname=ath1        -> connected_clients (wifi)",
			"GET /metro/<ip>[:<port>]?ifname=wlan0&freq=60 -> connected_clients_metro (60GHz)",
			"GET /device/<ip>[:<port>]/<acn path>          -> arbitrary acn endpoint",
			"GET /cache                                    -> learned ip -> working-port map",
			"GET /metrics                                  -> Prometheus metrics",
			"GET /healthz",
			"(port defaults to 10880 when omitted; a cached working port is forced)",
		},
	})
}
