package ssh

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const dialTimeout = 5 * time.Second

// SessionManager holds persistent SSH clients keyed by host address.
type SessionManager struct {
	clients map[string]*gossh.Client
	sshUser string
}

// NewSessionManager creates a new SessionManager with the given optional SSH user.
func NewSessionManager(sshUser string) *SessionManager {
	return &SessionManager{
		clients: make(map[string]*gossh.Client),
		sshUser: sshUser,
	}
}

// GetClient returns a cached or newly-dialed SSH client for host.
func (m *SessionManager) GetClient(ctx context.Context, host string) (*gossh.Client, error) {
	if c, ok := m.clients[host]; ok {
		return c, nil
	}

	cfg, err := buildClientConfig(m.sshUser)
	if err != nil {
		return nil, fmt.Errorf("failed to build SSH config: %w", err)
	}

	addr := net.JoinHostPort(host, "22")
	log.Printf("Opening SSH connection to %s", addr)

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connection to %s timed out or failed: %w", host, err)
	}

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake to %s failed: %w", host, err)
	}

	client := gossh.NewClient(sshConn, chans, reqs)
	log.Printf("SSH connection to %s established", host)
	m.clients[host] = client
	return client, nil
}

// RunCommand executes cmd on host and returns stdout (or stdout+stderr on failure).
func (m *SessionManager) RunCommand(ctx context.Context, host, cmd string) (string, error) {
	log.Printf("Running command on %s: %s", host, cmd)
	client, err := m.GetClient(ctx, host)
	if err != nil {
		return "", err
	}

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to open session on %s: %w", host, err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	runErr := sess.Run(cmd)

	if runErr == nil {
		return stdout.String(), nil
	}

	// Non-zero exit: if there's stderr return it as error; if only stdout, return stdout
	if stderr.Len() > 0 {
		return "", fmt.Errorf("command failed on %s: %s", host, stderr.String())
	}
	if stdout.Len() > 0 {
		// e.g. systemctl is-active returns non-zero but has useful output
		return stdout.String(), nil
	}
	return "", fmt.Errorf("command failed on %s: %w", host, runErr)
}

// CloseAll closes all open SSH clients.
func (m *SessionManager) CloseAll() {
	for host, c := range m.clients {
		log.Printf("Closing SSH connection to %s", host)
		c.Close()
	}
	m.clients = make(map[string]*gossh.Client)
}

// buildClientConfig constructs an ssh.ClientConfig with key-based auth + ssh-agent fallback.
func buildClientConfig(sshUser string) (*gossh.ClientConfig, error) {
	user := sshUser
	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = os.Getenv("LOGNAME")
		}
	}

	var authMethods []gossh.AuthMethod

	// Try ssh-agent first
	if agentAuth := agentAuthMethod(); agentAuth != nil {
		authMethods = append(authMethods, agentAuth)
	}

	// Try common key files
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		keyPath := filepath.Join(os.Getenv("HOME"), ".ssh", name)
		if signer, err := loadPrivateKey(keyPath); err == nil {
			authMethods = append(authMethods, gossh.PublicKeys(signer))
		}
	}

	hostKeyCallback, err := buildHostKeyCallback()
	if err != nil {
		// Fall back to accepting all host keys (same as Rust's KnownHosts::Accept)
		log.Printf("Warning: could not load known_hosts, accepting all host keys: %v", err)
		hostKeyCallback = gossh.InsecureIgnoreHostKey() //nolint:gosec
	}

	return &gossh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         dialTimeout,
	}, nil
}

func agentAuthMethod() gossh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return gossh.PublicKeysCallback(agent.NewClient(conn).Signers)
}

func loadPrivateKey(path string) (gossh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return gossh.ParsePrivateKey(data)
}

func buildHostKeyCallback() (gossh.HostKeyCallback, error) {
	khPath := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	return knownhosts.New(khPath)
}
