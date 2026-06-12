// Package bootstrap implements enrollment methods 1 and 2: adding a node by SSHing
// into it with a password or key, uploading the openbox binary, and launching the
// agent with a freshly minted enrollment token. After this one-time bootstrap the
// node manages its own identity and never needs the password/key again.
package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Options configures a remote bootstrap.
type Options struct {
	User       string
	Host       string // host or host:port
	Password   string
	KeyPath    string
	KeyPass    string
	BinaryPath string // openbox binary to upload (must match remote OS/arch)

	Server      string
	EnrollToken string
	AgentAddr   string
	Name        string
	Tags        []string

	Out io.Writer // progress output
}

// Result reports what was detected/done on the remote.
type Result struct {
	RemoteUname string
	InstallPath string
}

// Install connects to the remote host and bootstraps the agent.
func Install(ctx context.Context, opts Options) (*Result, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	authMethod, err := authFor(opts)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User: opts.User,
		Auth: []ssh.AuthMethod{authMethod},
		// TODO(security): pin/record the host key (known_hosts) instead of trusting
		// it blindly. Acceptable for a one-time bootstrap on a trusted network.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	addr := opts.Host
	if !strings.Contains(addr, ":") {
		addr += ":22"
	}
	fmt.Fprintf(out, "connecting to %s@%s …\n", opts.User, addr)
	cli, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	defer cli.Close()

	uname, err := runRemote(cli, "uname -s -m")
	if err != nil {
		return nil, fmt.Errorf("detect platform: %w", err)
	}
	uname = strings.TrimSpace(uname)
	fmt.Fprintf(out, "remote platform: %s\n", uname)

	bin, err := os.ReadFile(opts.BinaryPath)
	if err != nil {
		return nil, fmt.Errorf("read binary %s: %w", opts.BinaryPath, err)
	}
	fmt.Fprintf(out, "uploading openbox (%d KB) …\n", len(bin)/1024)
	const installPath = "$HOME/.openbox/bin/openbox"
	upload := "mkdir -p $HOME/.openbox/bin && cat > $HOME/.openbox/bin/openbox.tmp" +
		" && chmod +x $HOME/.openbox/bin/openbox.tmp" +
		" && mv $HOME/.openbox/bin/openbox.tmp $HOME/.openbox/bin/openbox"
	if err := runRemoteStdin(cli, upload, bytes.NewReader(bin)); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	fmt.Fprintf(out, "launching agent …\n")
	launch := buildLaunch(installPath, opts)
	if _, err := runRemote(cli, launch); err != nil {
		return nil, fmt.Errorf("launch agent: %w", err)
	}

	return &Result{RemoteUname: uname, InstallPath: installPath}, nil
}

// buildLaunch constructs the detached agent launch command.
func buildLaunch(installPath string, opts Options) string {
	args := []string{
		installPath, "agent",
		"--server", shellQuote(opts.Server),
		"--token", shellQuote(opts.EnrollToken),
		"--addr", shellQuote(opts.AgentAddr),
	}
	if opts.Name != "" {
		args = append(args, "--name", shellQuote(opts.Name))
	}
	for _, t := range opts.Tags {
		args = append(args, "--tag", shellQuote(t))
	}
	cmd := strings.Join(args, " ")
	// Detach so it survives the SSH session closing.
	return "nohup " + cmd + " >$HOME/.openbox/agent.boot.log 2>&1 </dev/null & echo launched"
}

func authFor(opts Options) (ssh.AuthMethod, error) {
	if opts.KeyPath != "" {
		key, err := os.ReadFile(opts.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		var signer ssh.Signer
		if opts.KeyPass != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(opts.KeyPass))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	}
	if opts.Password != "" {
		return ssh.Password(opts.Password), nil
	}
	return nil, fmt.Errorf("no auth provided: pass --password or --key")
}

func runRemote(cli *ssh.Client, cmd string) (string, error) {
	sess, err := cli.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = &buf
	err = sess.Run(cmd)
	return buf.String(), err
}

func runRemoteStdin(cli *ssh.Client, cmd string, stdin io.Reader) error {
	sess, err := cli.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = stdin
	var buf bytes.Buffer
	sess.Stderr = &buf
	if err := sess.Run(cmd); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}
	return nil
}

// shellQuote single-quotes a value for safe inclusion in a remote sh command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
