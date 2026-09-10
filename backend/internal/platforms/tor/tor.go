// Package tor manages the Tor daemon as a supervised subprocess and provides a
// SOCKS5 HTTP client plus circuit rotation.
package tor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"meshdepot/internal/safego"
)

const (
	// controlPassword and its hash, as in the previous tor container.
	controlPassword = "meshdepot"
	hashedPassword  = "16:C9A55C897571F08260917354D70C4C1154427E7F361B74FF64DCA19B37"
)

// The container shares the host's network stack, so these are host ports: one
// contiguous block with the app (9000) and the Firefox resolver (9001) rather
// than Tor's defaults, which would collide with a Tor already on the machine.
// Overridable so such a deployment can move them without a rebuild. SocksAddr is
// exported because the admin health check dials it.
var (
	SocksAddr   = addressFromEnvironment("TOR_SOCKS_ADDR", "127.0.0.1:9002")
	controlAddr = addressFromEnvironment("TOR_CONTROL_ADDR", "127.0.0.1:9003")
)

// addressFromEnvironment treats a blank value as unset: an empty variable in a
// compose file is a mistake, not an instruction.
func addressFromEnvironment(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

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

// Start runs tor and a watchdog that restarts it on exit, until ctx is cancelled.
// It returns once tor has bootstrapped, or on timeout.
func (supervisor *Supervisor) Start(ctx context.Context) error {
	_ = os.MkdirAll(supervisor.dataDir, 0o700)
	safego.Go("tor-watchdog", func() { supervisor.supervise(ctx) })
	if failure := supervisor.waitBootstrap(30 * time.Second); failure != nil {
		return failure
	}
	return nil
}

func (supervisor *Supervisor) supervise(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		command := exec.CommandContext(ctx, supervisor.binaryPath,
			"--SocksPort", SocksAddr,
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

func (supervisor *Supervisor) waitBootstrap(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, failure := net.DialTimeout("tcp", SocksAddr, time.Second)
		if failure == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("tor: SOCKS port %s not reachable after %s", SocksAddr, timeout)
}

func (supervisor *Supervisor) HTTPClient(timeout time.Duration) (*http.Client, error) {
	dialer, failure := proxy.SOCKS5("tcp", SocksAddr, nil, proxy.Direct)
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

func (supervisor *Supervisor) NewCircuit() error {
	conn, failure := net.DialTimeout("tcp", controlAddr, 3*time.Second)
	if failure != nil {
		return failure
	}
	defer conn.Close()
	_, failure = fmt.Fprintf(conn, "AUTHENTICATE \"%s\"\r\nSIGNAL NEWNYM\r\nQUIT\r\n", controlPassword)
	return failure
}
