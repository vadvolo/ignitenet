# ignitenet-proxy — Client / Agent Usage Guide

An HTTP proxy that fetches live data from IgniteNet radios. You give it a device
IP, it authenticates for you and returns JSON. No login, cookies, `stok`
tokens, or SSH handling on your side.

## Base URL

```
http://<proxy-host>:8881
```

All endpoints are plain HTTP `GET`; no auth required from the caller.

## Endpoints

| Method & path | Returns |
|---------------|---------|
| `GET /healthz` | `ok` — liveness check |
| `GET /rssi/<ip>[:<port>]` | Normalized wifi+metro signal summary — use this for RSSI |
| `GET /clients/<ip>[:<port>]?ifname=ath1` | Raw connected wifi clients on an interface |
| `GET /metro/<ip>[:<port>]?ifname=wlan0&freq=60` | Raw 60GHz/metro link |
| `GET /device/<ip>[:<port>]/<acn path>` | Arbitrary acn passthrough |
| `GET /cache` | Learned `ip -> working-port` map |
| `GET /metrics` | Prometheus metrics |

- `<ip>` must be a valid IP, else `400 {"error":"invalid device IP"}`.
- `<port>` is optional, defaults to `10880`. Supply only for non-standard ports,
  inline (`/rssi/<ip>:10880`) or as `?port=10880`.

## Getting signal strength (RSSI in dBm) — use `/rssi/<ip>`

The proxy probes the wifi interfaces (`ath0`, `ath1`) and the 60GHz/metro
interface (`wlan0`) and returns one merged object. Interfaces with no clients
are omitted.

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

- **wifi**: `signal` is per-chain RSSI (dBm); `signal_combined` is the combined
  RSSI (dBm). One entry per connected client per interface.
- **metro** (60GHz point-to-point): `local_rssi` is what this radio hears,
  `remote_rssi` is what the far end hears (both dBm); `link_distance` metres.
- dBm closer to `0` is stronger (`-52` beats `-80`).

## Errors & port caching

- `400 {"error":...}` — bad IP/port.
- `502 {"error":"...try another port..."}` — device unreachable or login failed;
  the cached port was cleared. Retry with a different `<port>`; once one works it
  is cached and forced on later calls (`GET /cache`). You do not need to probe
  ports yourself under normal operation.
- `502` with a passthrough body — the device itself returned an error.

Use a request timeout of ~25s; the first call to a device is slower (login +
interface probing).
