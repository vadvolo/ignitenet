package main

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics, ignitenet_proxy_ prefix, low-cardinality labels.
var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ignitenet_proxy_http_requests_total",
		Help: "Total HTTP requests handled by the proxy, by route, method and status code.",
	}, []string{"route", "method", "code"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ignitenet_proxy_http_request_duration_seconds",
		Help:    "HTTP request handling duration in seconds, by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	deviceFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ignitenet_proxy_device_fetch_total",
		Help: "Device fetch attempts by outcome (success, http_error, unreachable).",
	}, []string{"outcome"})

	deviceFetchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "ignitenet_proxy_device_fetch_duration_seconds",
		Help:    "Time spent fetching from the device (login + request) in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	portCacheSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ignitenet_proxy_port_cache_size",
		Help: "Number of device IPs with a cached working port.",
	})
)

func routeOf(path string) string {
	switch {
	case path == "/healthz":
		return "/healthz"
	case path == "/metrics":
		return "/metrics"
	case path == "/cache":
		return "/cache"
	case strings.HasPrefix(path, "/device/"):
		return "/device"
	case strings.HasPrefix(path, "/rssi/"):
		return "/rssi"
	case strings.HasPrefix(path, "/clients/"):
		return "/clients"
	case strings.HasPrefix(path, "/metro/"):
		return "/metro"
	case path == "/":
		return "/"
	default:
		return "other"
	}
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := routeOf(r.URL.Path)
		dur := time.Since(start)
		httpRequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(rec.code)).Inc()
		httpRequestDuration.WithLabelValues(route).Observe(dur.Seconds())
		log.Printf("%s %s %d %s", r.Method, r.URL.RequestURI(), rec.code, dur.Round(time.Millisecond))
	})
}
