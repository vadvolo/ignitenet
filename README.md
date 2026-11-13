# ignitenet-proxy

A tiny HTTP service that pulls live data from IgniteNet radios. You give it a
device IP, it logs in to the device and returns the JSON the IgniteNet web UI
(the LuCI-based "acn" interface) would show — including connected clients and
60GHz/metro link signal — plus a normalized RSSI summary.

It can tunnel every connection through an SSH jump host, or dial directly when
run on a host that already sees the devices.

## How it works

1. Opens a single SSH connection to a jump host (key auth) and reuses it — or
   dials directly when `DIRECT=1`.
2. Connects to `http://<device-ip>:10880`.
3. Authenticates: `POST /cgi-bin/acn/` with `username`/`password`. The device
   replies `302` with a `sysauth` cookie and a session token (`stok`) in the
   `Location` header.
4. Fetches resources at `/cgi-bin/acn/;stok=<stok>/<path>` with the `sysauth`
   cookie. An expired session is served the HTML login page (HTTP 200) instead
   of JSON — the proxy detects that and re-authenticates once.

The session (`stok` + cookie) is cached per `ip:port`. The proxy also remembers,
per IP, the **port** where login last succeeded and forces it on later requests;
if the device becomes unreachable the cached port is dropped and a `502` is
returned so the caller can retry with another port. Inspect via `GET /cache`.

## Configuration (`.env` or environment)

See `.env.example` for the full list.

| Variable       | Default         | Description                                   |
|----------------|-----------------|-----------------------------------------------|
| `USERNAME`     | `root`          | Device login username                         |
| `PASSWORD`     | — (required)    | Device login password                         |
| `LISTEN`       | `:8881`         | HTTP listen address                           |
| `DIRECT`       | `0`             | `1` = dial devices directly (skip SSH tunnel) |
| `JUMP_ADDR`    | — (required unless `DIRECT=1`) | SSH jump host `host:port`      |
| `JUMP_USER`    | `root`          | SSH jump host user (tunnel mode)              |
| `SSH_KEY`      | `~/.ssh/id_rsa` | Private key for the jump host (tunnel mode)   |
| `WIFI_IFACES`  | `ath0,ath1`     | wifi interfaces probed by `/rssi`             |
| `METRO_IFACES` | `wlan0`         | 60GHz/metro interfaces probed by `/rssi`      |

## Run

```sh
go build -o ignitenet-proxy .
PASSWORD='...' DIRECT=1 ./ignitenet-proxy
# or, tunneling:
PASSWORD='...' JUMP_ADDR=jump.example:22 ./ignitenet-proxy
```

## Endpoints

```sh
# Normalized, merged RSSI summary (wifi + metro):
curl http://localhost:8881/rssi/<device-ip>

# Connected wifi clients on an interface:
curl "http://localhost:8881/clients/<device-ip>?ifname=ath1"

# 60GHz / metro link:
curl "http://localhost:8881/metro/<device-ip>?ifname=wlan0&freq=60"

# Arbitrary acn path passthrough:
curl "http://localhost:8881/device/<device-ip>/admin/status/connected_clients?ifname=ath1"

# Learned ip -> working-port map / metrics / health:
curl http://localhost:8881/cache
curl http://localhost:8881/metrics
curl http://localhost:8881/healthz
```

Errors are returned as `{"error": "..."}`. If the device answers with an error
body it is passed through with a `502`. The HTTP port defaults to `10880`;
override per request with `<ip>:<port>` or `?port=`.

## Monitoring & deploy

`GET /metrics` exposes Prometheus series (`ignitenet_proxy_` prefix). Logs go to
stdout (journald under systemd) and can ship to Loki with Promtail. See
`deploy/monitoring/`. `deploy/deploy.sh` installs a hardened systemd unit over
SSH:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ignitenet-proxy .
DEPLOY_HOST=root@your-host IGN_PASSWORD='...' ./deploy/deploy.sh ./ignitenet-proxy
```

## License

MIT — see [LICENSE](LICENSE).
