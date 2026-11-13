package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all tunables for the proxy, sourced from the environment / .env.
type Config struct {
	Listen      string   // HTTP listen address, e.g. ":8881"
	Username    string   // IgniteNet login username
	Password    string   // IgniteNet login password
	Direct      bool     // dial devices directly instead of via the SSH jump host
	JumpAddr    string   // SSH jump host host:port
	JumpUser    string   // SSH jump host user
	SSHKeyPath  string   // private key used to reach the jump host
	DevicePort  int      // IgniteNet HTTP port
	WifiIfaces  []string // default wifi interfaces probed by /rssi
	MetroIfaces []string // default 60GHz/metro interfaces probed by /rssi
}

// loadDotEnv reads simple KEY=VALUE lines from path into the process
// environment without overwriting variables that are already set. Missing
// file is not an error.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "direct":
		return true
	}
	return false
}

// splitList parses a comma/space separated list, dropping empties.
func splitList(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func loadConfig() (Config, error) {
	_ = loadDotEnv(".env")

	home, _ := os.UserHomeDir()
	defaultKey := filepath.Join(home, ".ssh", "id_rsa")

	cfg := Config{
		Listen:      env("LISTEN", ":8881"),
		Username:    env("USERNAME", "root"),
		Password:    os.Getenv("PASSWORD"),
		Direct:      isTrue(os.Getenv("DIRECT")),
		JumpAddr:    os.Getenv("JUMP_ADDR"),
		JumpUser:    env("JUMP_USER", "root"),
		SSHKeyPath:  env("SSH_KEY", defaultKey),
		DevicePort:  10880,
		WifiIfaces:  splitList(env("WIFI_IFACES", "ath0,ath1")),
		MetroIfaces: splitList(env("METRO_IFACES", "wlan0")),
	}

	if cfg.Password == "" {
		return cfg, fmt.Errorf("PASSWORD is not set (put it in .env or the environment)")
	}
	if !cfg.Direct && cfg.JumpAddr == "" {
		return cfg, fmt.Errorf("set JUMP_ADDR (host:port of an SSH jump host) or DIRECT=1 to dial devices directly")
	}
	return cfg, nil
}
