# Monitoring

`ignitenet-proxy` exposes Prometheus metrics at `GET /metrics` and logs to
stdout (captured by journald under systemd).

## Metrics

| Series | Meaning |
|--------|---------|
| `ignitenet_proxy_http_requests_total{route,method,code}` | Requests to the proxy |
| `ignitenet_proxy_http_request_duration_seconds{route}` | Proxy request latency |
| `ignitenet_proxy_device_fetch_total{outcome}` | Device fetches (`success`/`http_error`/`unreachable`) |
| `ignitenet_proxy_device_fetch_duration_seconds` | Upstream device latency |
| `ignitenet_proxy_port_cache_size` | IPs with a cached working port |

Add `prometheus-job.yml` to your Prometheus `scrape_configs` and reload.

## Logs

Merge `promtail-ignitenet-proxy.yaml` into your Promtail config (point `clients`
at your Loki). Logs are then queryable as `{job="ignitenet-proxy"}`.

## Alerting (optional)

```yaml
- alert: IgnitenetProxyDown
  expr: up{job="ignitenet-proxy"} == 0
  for: 5m
  labels: { severity: critical }
  annotations: { summary: "ignitenet-proxy {{ $labels.instance }} is down" }
```
