# HOW-TO: Integrate with ignitenet-proxy

Practical guide for pulling live data from IgniteNet radios through
`ignitenet-proxy`. You talk plain HTTP to the proxy; it handles device login
(`sysauth` cookie + `stok` token), sessions, re-auth, and port discovery for
you. No credentials, cookies, or SSH on the caller side.

For the terse endpoint reference see [`BOT_USAGE.md`](./BOT_USAGE.md). This
document focuses on *integrating* the service into scripts, pollers and
monitoring.

## 1. Base URL

```
http://<proxy-host>:8881
```

Everything is `GET`, no auth required from the caller. Health: `GET /healthz`.

> **Egress note.** If your client runs in a sandbox with a deny-by-default
> network policy, port `8881` to the proxy host must be in the allowed outbound
> destinations, or requests fail before leaving the client. This is a
> client-side policy change; the proxy itself is open.

## 2. The one call you usually want: `/rssi/<ip>`

Returns a normalized, merged view of both the WiFi radios and the 60GHz/metro
link — already decoded, no device-specific parsing.

```bash
curl -s http://<proxy-host>:8881/rssi/<device-ip> | jq
```

```json
{
  "ip": "<device-ip>",
  "wifi": [
    { "ifname": "ath1", "mac": "...", "name": "N/A", "ip": "0.0.0.0",
      "signal": [-83, -80], "signal_combined": -78, "tx_rate": 78, "rx_rate": 78 }
  ],
  "metro": [
    { "ifname": "wlan0", "mac": "...", "hostname": "...",
      "local_rssi": -52, "remote_rssi": -54,
      "local_rssi_percentage": 64, "remote_rssi_percentage": 60,
      "link_distance": 551, "tx_rate": 4620, "rx_rate": 4620 }
  ]
}
```

- **wifi[]** — one per connected client per radio. `signal` = per-chain RSSI
  (dBm); `signal_combined` = combined RSSI (dBm).
- **metro[]** — the 60GHz point-to-point link. `local_rssi` = what this radio
  hears; `remote_rssi` = what the far end hears (dBm). `link_distance` metres.
- dBm closer to `0` is stronger (`-52` beats `-80`).
- Empty radios/interfaces are omitted (not an error).

## 3. Ports: you don't probe, the proxy does

The device web port defaults to `10880`. Omit it and the proxy uses the default,
caching whatever port actually logged in, per device IP. Later calls for that IP
are forced onto the cached port. If the device becomes unreachable you get
`502 {"error":"... try another port ..."}` and the cache is cleared — retry with
an alternate port (`/rssi/<device-ip>:443`). See the learned map via `GET /cache`.

## 4. Scripting examples

### bash — collect RSSI for a list of devices

```bash
#!/usr/bin/env bash
BASE=http://<proxy-host>:8881
for ip in 10.0.0.11 10.0.0.12; do
  curl -fsS -m 25 "$BASE/rssi/$ip" \
    | jq -c --arg ip "$ip" '{ip:$ip,
        metro_local: (.metro[0].local_rssi // null),
        metro_remote:(.metro[0].remote_rssi // null),
        wifi_combined:[.wifi[].signal_combined]}'
done
```

### python — pull and flatten for storage/metrics

```python
import requests
BASE = "http://<proxy-host>:8881"

def rssi(ip, timeout=25):
    r = requests.get(f"{BASE}/rssi/{ip}", timeout=timeout)
    if r.status_code == 502:                      # unreachable on cached/default port
        r = requests.get(f"{BASE}/rssi/{ip}:443", timeout=timeout)  # retry alt port
    r.raise_for_status()
    return r.json()

d = rssi("10.0.0.11")
for m in d["metro"]:
    print(d["ip"], m["ifname"], "local", m["local_rssi"], "remote", m["remote_rssi"])
for w in d["wifi"]:
    print(d["ip"], w["ifname"], w["mac"], "combined", w["signal_combined"], "chains", w["signal"])
```

## 5. Scheduled collection

Poll on a timer; the session + port cache make repeat calls cheap (no re-login).
Keep a per-request timeout of ~25s — the *first* call to a device is slower
(login + interface probing); cached calls are sub-second.

## 6. Monitoring

- **Metrics:** `GET /metrics` (Prometheus): `ignitenet_proxy_http_requests_total`,
  `ignitenet_proxy_device_fetch_total{outcome=...}`,
  `ignitenet_proxy_device_fetch_duration_seconds`, `ignitenet_proxy_port_cache_size`.
- **Logs:** journald → Loki, query `{job="ignitenet-proxy"}` (see `deploy/monitoring/`).

## 7. Error contract

| Status | Meaning | Action |
|--------|---------|--------|
| `200` | JSON body | use it |
| `400 {"error":...}` | bad IP/port | fix the request |
| `502 {"error":"...try another port..."}` | unreachable / login failed; cache cleared | retry with an alternate port, then treat as down |
| `502` + passthrough body | the device itself returned an error | inspect the body |
