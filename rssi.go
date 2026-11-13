package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RSSIReport is the normalized signal summary for an IgniteNet device, merging
// the wifi (connected_clients) and 60GHz/metro (connected_clients_metro) views.
type RSSIReport struct {
	IP    string       `json:"ip"`
	Wifi  []WifiEntry  `json:"wifi"`
	Metro []MetroEntry `json:"metro"`
}

type WifiEntry struct {
	Ifname         string  `json:"ifname"`
	Mac            string  `json:"mac"`
	Name           string  `json:"name"`
	IP             string  `json:"ip"`
	Signal         []int   `json:"signal"`          // per-chain RSSI, dBm
	SignalCombined int     `json:"signal_combined"` // combined RSSI, dBm
	TxRate         float64 `json:"tx_rate"`
	RxRate         float64 `json:"rx_rate"`
}

type MetroEntry struct {
	Ifname        string  `json:"ifname"`
	Mac           string  `json:"mac"`
	Hostname      string  `json:"hostname"`
	LocalRSSI     int     `json:"local_rssi"`  // dBm
	RemoteRSSI    int     `json:"remote_rssi"` // dBm
	LocalPercent  int     `json:"local_rssi_percentage"`
	RemotePercent int     `json:"remote_rssi_percentage"`
	LinkDistance  int     `json:"link_distance"`
	TxRate        float64 `json:"tx_rate"`
	RxRate        float64 `json:"rx_rate"`
}

// clientsEnvelope decodes {"clients": [ ... ]} where "clients" may also be an
// empty object {} when there are none.
type clientsEnvelope struct {
	Clients json.RawMessage `json:"clients"`
}

func decodeClients[T any](body []byte) ([]T, error) {
	var env clientsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(env.Clients))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, nil // "{}" or null -> no clients
	}
	var out []T
	if err := json.Unmarshal(env.Clients, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// rawWifi / rawMetro mirror the device JSON field names.
type rawWifi struct {
	Mac            string  `json:"mac"`
	Name           string  `json:"name"`
	IP             string  `json:"ip"`
	Signal         []int   `json:"signal"`
	SignalCombined int     `json:"signalCombined"`
	TxRate         float64 `json:"tx_rate"`
	RxRate         float64 `json:"rx_rate"`
}

type rawMetro struct {
	Mac           string  `json:"mac"`
	Hostname      string  `json:"hostname"`
	LocalRSSI     int     `json:"local_rssi"`
	RemoteRSSI    int     `json:"remote_rssi"`
	LocalPercent  int     `json:"local_rssi_percentage"`
	RemotePercent int     `json:"remote_rssi_percentage"`
	LinkDistance  int     `json:"linkDistance"`
	TxRate        float64 `json:"tx_rate"`
	RxRate        float64 `json:"rx_rate"`
}

// RSSI builds a normalized report by probing the configured wifi and metro
// interfaces. Interfaces that error or have no clients are simply skipped; it
// only fails if the device could not be reached at all.
func (c *Client) RSSI(ip, port string) (RSSIReport, int, error) {
	rep := RSSIReport{IP: ip, Wifi: []WifiEntry{}, Metro: []MetroEntry{}}
	var firstErr error
	reached := false

	for _, ifn := range c.cfg.WifiIfaces {
		body, _, err := c.Fetch(ip, port, "admin/status/connected_clients?ifname="+ifn)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		reached = true
		clients, err := decodeClients[rawWifi](body)
		if err != nil {
			continue
		}
		for _, w := range clients {
			rep.Wifi = append(rep.Wifi, WifiEntry{
				Ifname: ifn, Mac: w.Mac, Name: w.Name, IP: w.IP,
				Signal: w.Signal, SignalCombined: w.SignalCombined,
				TxRate: w.TxRate, RxRate: w.RxRate,
			})
		}
	}

	for _, ifn := range c.cfg.MetroIfaces {
		body, _, err := c.Fetch(ip, port, "admin/status/connected_clients_metro?ifname="+ifn+"&freq=60")
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		reached = true
		clients, err := decodeClients[rawMetro](body)
		if err != nil {
			continue
		}
		for _, m := range clients {
			rep.Metro = append(rep.Metro, MetroEntry{
				Ifname: ifn, Mac: m.Mac, Hostname: m.Hostname,
				LocalRSSI: m.LocalRSSI, RemoteRSSI: m.RemoteRSSI,
				LocalPercent: m.LocalPercent, RemotePercent: m.RemotePercent,
				LinkDistance: m.LinkDistance, TxRate: m.TxRate, RxRate: m.RxRate,
			})
		}
	}

	if !reached && firstErr != nil {
		return rep, http.StatusBadGateway, firstErr
	}
	return rep, http.StatusOK, nil
}
