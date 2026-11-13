package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Tunnel maintains a single SSH connection to the jump host and dials target
// devices through it. The connection is established lazily and transparently
// re-established if it drops.
type Tunnel struct {
	user   string
	addr   string
	signer ssh.Signer

	mu     sync.Mutex
	client *ssh.Client
}

func NewTunnel(user, addr, keyPath string) (*Tunnel, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh key %s: %w", keyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key %s: %w", keyPath, err)
	}
	return &Tunnel{user: user, addr: addr, signer: signer}, nil
}

func (t *Tunnel) connect() (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            t.user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(t.signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // jump host inside trusted VPN
		Timeout:         10 * time.Second,
	}
	return ssh.Dial("tcp", t.addr, cfg)
}

// clientFor returns a live SSH client, reconnecting if the previous one is gone.
func (t *Tunnel) clientFor() (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return t.client, nil
	}
	c, err := t.connect()
	if err != nil {
		return nil, err
	}
	t.client = c
	return c, nil
}

// reset drops the cached client so the next dial reconnects.
func (t *Tunnel) reset(bad *ssh.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client == bad {
		if t.client != nil {
			t.client.Close()
		}
		t.client = nil
	}
}

// DialContext dials addr on the far side of the jump host. It retries once
// through a fresh SSH connection if the first attempt fails (stale tunnel).
func (t *Tunnel) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	for attempt := 0; attempt < 2; attempt++ {
		client, err := t.clientFor()
		if err != nil {
			return nil, err
		}
		conn, err := client.Dial(network, addr)
		if err != nil {
			t.reset(client)
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("ssh tunnel to %s via %s unavailable", addr, t.addr)
}

func (t *Tunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		t.client.Close()
		t.client = nil
	}
}
