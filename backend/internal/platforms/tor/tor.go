// Package tor manages the Tor daemon as a supervised subprocess inside the app
// container and provides a SOCKS5 HTTP client plus circuit rotation (replacement
// for the separate tor container).
package tor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"golang.org/x/net/proxy"

	"meshdepot/internal/safego"
)

const (
	socksAddr   = "127.0.0.1:9050"
	controlAddr = "127.0.0.1:9051"
	// controlPassword + matching hash (as in the previous tor container).
	controlPassword = "meshdepot"
	hashedPassword  = "16:C9A55C897571F08260917354D70C4C1154427E7F361B74FF64DCA19B37"
)

// Supervisor starts and supervises the tor daemon.
type Supervisor struct {
	binaryPath string
	dataDir    string
}

func New(binaryPath string) *Supervisor {
	if binaryPath == "" {
		binaryPath = "tor"
	}
	return &Supervisor{binaryPath: binaryPath, dataDir: "/tmp/meshdepot-tor"}
}

// Start starts tor and a watchdog that restarts it on exit. It runs until ctx
// is cancelled. It returns once tor has bootstrapped (or on timeout).
func (supervisor *Supervisor) Start(ctx context.Context) error {
	_ = os.MkdirAll(supervisor.dataDir, 0o700)
	safego.Go("tor-watchdog", func() { supervisor.supervise(ctx) })
	if failure := supervisor.waitBootstrap(30 * time.Second); failure != nil {
		return failure
	}
	return nil
}

// supervise starts tor and restarts it on an unexpected exit.
func (supervisor *Supervisor) supervise(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		command := exec.CommandContext(ctx, supervisor.binaryPath,
			"--SocksPort", socksAddr,
			"--ControlPort", controlAddr,
			"--HashedControlPassword", hashedPassword,
			"--DataDirectory", supervisor.dataDir,
			"--MaxCircuitDirtiness", "60",
			"--NewCircuitPeriod", "60",
			"--Log", "warn stderr",
		)
		command.Stdout = os.Stderr
		command.Stderr = os.Stderr
		_ = command.Run() // blocks until exit
		if ctx.Err() != nil {
			return
		}
		time.Sleep(2 * time.Second) // backoff before restart
	}
}

// waitBootstrap polls until the SOCKS port accepts connections.
func (supervisor *Supervisor) waitBootstrap(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, failure := net.DialTimeout("tcp", socksAddr, time.Second)
		if failure == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("tor: SOCKS port %s not reachable after %s", socksAddr, timeout)
}

// HTTPClient returns an HTTP client that goes through the Tor SOCKS5 proxy.
func (supervisor *Supervisor) HTTPClient(timeout time.Duration) (*http.Client, error) {
	dialer, failure := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if failure != nil {
		return nil, failure
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
				return contextDialer.DialContext(ctx, network, addr)
			}
			return dialer.Dial(network, addr)
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

// NewCircuit requests a new Tor route (new IP) via the control port.
func (supervisor *Supervisor) NewCircuit() error {
	conn, failure := net.DialTimeout("tcp", controlAddr, 3*time.Second)
	if failure != nil {
		return failure
	}
	defer conn.Close()
	_, failure = fmt.Fprintf(conn, "AUTHENTICATE \"%s\"\r\nSIGNAL NEWNYM\r\nQUIT\r\n", controlPassword)
	return failure
}
