package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"openbox.io/openbox/internal/agent"
	"openbox.io/openbox/internal/bootstrap"
	"openbox.io/openbox/internal/ca"
	"openbox.io/openbox/internal/config"
	"openbox.io/openbox/internal/control"
	"openbox.io/openbox/internal/cpclient"
	"openbox.io/openbox/internal/store"
)

// stringSlice is a repeatable string flag (e.g. --tag a --tag b).
type stringSlice []string

func (s *stringSlice) String() string     { return fmt.Sprint(*s) }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// --- login / whoami ---

func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", defaultControlURL, "control-plane base URL")
	token := fs.String("token", "", "user API token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *token == "" {
		return fail(fmt.Errorf("--token is required"))
	}

	ctx, stop := signalCtx()
	defer stop()

	// Verify the credentials before saving them.
	who, err := cpclient.New(*server, *token).Whoami(ctx)
	if err != nil {
		return fail(fmt.Errorf("login failed: %w", err))
	}

	cfg, err := config.LoadClient()
	if err != nil {
		return fail(err)
	}
	cfg.Server, cfg.Token, cfg.User = *server, *token, who.Name
	if err := cfg.Save(); err != nil {
		return fail(err)
	}
	fmt.Printf("logged in as %s on %s\n", who.Name, *server)
	return 0
}

func cmdWhoami(args []string) int {
	cfg, err := config.LoadClient()
	if err != nil {
		return fail(err)
	}
	if !cfg.LoggedIn() {
		return fail(fmt.Errorf("not logged in"))
	}
	ctx, stop := signalCtx()
	defer stop()
	who, err := cpclient.New(cfg.Server, cfg.Token).Whoami(ctx)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("%s (%s) @ %s\n", who.Name, who.UserID, cfg.Server)
	return 0
}

// --- control plane ---

func cmdControl(args []string) int {
	fs := flag.NewFlagSet("control", flag.ContinueOnError)
	addr := fs.String("addr", defaultControlAddr, "listen address")
	url := fs.String("url", defaultControlURL, "public base URL advertised to nodes")
	dbPath := fs.String("db", filepath.Join(config.Home(), "control", "openbox.db"), "sqlite database path")
	caPath := fs.String("ca", filepath.Join(config.Home(), "control", "ca_key"), "CA private key path")
	mesh := meshFlags{}
	fs.BoolVar(&mesh.enabled, "mesh", false, "join the mesh so the web console can reach mesh nodes")
	fs.StringVar(&mesh.control, "mesh-control", "", "coordination server URL (Headscale/Tailscale)")
	fs.StringVar(&mesh.authKey, "mesh-authkey", "", "mesh pre-auth key (first join only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signalCtx()
	defer stop()

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		return fail(err)
	}
	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	authority, err := ca.LoadOrCreate(*caPath)
	if err != nil {
		return fail(err)
	}

	if token, err := control.EnsureBootstrapUser(ctx, st, "me"); err != nil {
		return fail(err)
	} else if token != "" {
		fmt.Printf("\n  ┌─ openbox control plane initialized ─────────────────────────\n")
		fmt.Printf("  │ A user was created. Log in from any machine with:\n")
		fmt.Printf("  │\n")
		fmt.Printf("  │   openbox login --server %s --token %s\n", *url, token)
		fmt.Printf("  └─────────────────────────────────────────────────────────────\n\n")
	}

	tr, err := buildTransport("control", mesh)
	if err != nil {
		return fail(err)
	}
	defer tr.Close()

	srv := control.New(st, authority, *url, tr)
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler()}
	go func() { <-ctx.Done(); httpSrv.Close() }()

	fmt.Printf("openbox control plane listening on %s (public %s)\n", *addr, *url)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fail(err)
	}
	return 0
}

// --- agent ---

func cmdAgent(args []string) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	addr := fs.String("addr", defaultAgentAddr, "listen/advertise address")
	server := fs.String("server", os.Getenv("OPENBOX_SERVER"), "control-plane URL (first registration only)")
	token := fs.String("token", os.Getenv("OPENBOX_ENROLL_TOKEN"), "enrollment token (first registration only)")
	name := fs.String("name", "", "node display name (default: hostname)")
	var tags stringSlice
	fs.Var(&tags, "tag", "tag to request at registration (repeatable)")
	mesh := meshFlags{}
	fs.BoolVar(&mesh.enabled, "mesh", false, "join the Tailscale overlay (peer-to-peer over NAT)")
	fs.StringVar(&mesh.control, "mesh-control", "", "coordination server URL (Headscale/Tailscale)")
	fs.StringVar(&mesh.authKey, "mesh-authkey", "", "mesh pre-auth key (first join only)")
	fs.StringVar(&mesh.hostname, "mesh-hostname", "", "node name on the tailnet")
	fs.BoolVar(&mesh.verbose, "mesh-verbose", false, "surface tailscale logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signalCtx()
	defer stop()

	tr, err := buildTransport("agent", mesh)
	if err != nil {
		return fail(err)
	}
	defer tr.Close()

	listenAddr := *addr
	if mesh.enabled || os.Getenv("OPENBOX_MESH") != "" {
		listenAddr = meshListenAddr(*addr) // bind on all tailnet addresses
	}

	err = agent.Run(ctx, agent.Options{
		Transport:   tr,
		Addr:        listenAddr,
		Server:      *server,
		EnrollToken: *token,
		Name:        *name,
		Tags:        tags,
	})
	if err != nil {
		return fail(err)
	}
	return 0
}

// meshListenAddr reduces an address to ":port" so the tsnet listener binds on all
// tailnet addresses rather than a host-local one.
func meshListenAddr(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return ":" + port
	}
	return addr
}

// --- node token ---

func cmdNode(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: openbox node <token|add> ...")
		return 2
	}
	switch args[0] {
	case "token":
		return cmdNodeToken(args)
	case "add":
		return cmdNodeAdd(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: openbox node <token|add> ...")
		return 2
	}
}

func cmdNodeToken(args []string) int {
	fs := flag.NewFlagSet("node token", flag.ContinueOnError)
	ttl := fs.Duration("ttl", 15*time.Minute, "token lifetime")
	var tags stringSlice
	fs.Var(&tags, "tag", "tag to assign to the node (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	cfg, err := config.LoadClient()
	if err != nil {
		return fail(err)
	}
	if !cfg.LoggedIn() {
		return fail(fmt.Errorf("not logged in"))
	}

	ctx, stop := signalCtx()
	defer stop()

	resp, err := cpclient.New(cfg.Server, cfg.Token).CreateEnrollToken(ctx, tags, *ttl)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("enrollment token (valid %s):\n\n", ttl)
	fmt.Printf("  run this on the node you want to add:\n\n")
	tagArgs := ""
	for _, t := range tags {
		tagArgs += " --tag " + t
	}
	fmt.Printf("    openbox agent --server %s --token %s%s\n\n", resp.Server, resp.Token, tagArgs)
	return 0
}

// cmdNodeAdd implements enrollment methods 1 & 2: bootstrap a remote node over SSH.
func cmdNodeAdd(args []string) int {
	fs := flag.NewFlagSet("node add", flag.ContinueOnError)
	host := fs.String("host", "", "user@host[:port] to bootstrap")
	password := fs.String("password", "", "SSH password (method 1)")
	keyPath := fs.String("key", "", "SSH private key path (method 2)")
	keyPass := fs.String("key-pass", "", "passphrase for the private key")
	binary := fs.String("binary", "", "openbox binary to upload (default: this binary; must match remote OS/arch)")
	agentAddr := fs.String("agent-addr", defaultAgentAddr, "address the agent listens on/advertises")
	name := fs.String("name", "", "node display name")
	var tags stringSlice
	fs.Var(&tags, "tag", "tag to assign (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	user, hostport, ok := strings.Cut(*host, "@")
	if *host == "" || !ok {
		return fail(fmt.Errorf("--host must be user@host[:port]"))
	}
	if *password == "" && *keyPath == "" {
		return fail(fmt.Errorf("provide --password or --key"))
	}

	cfg, err := config.LoadClient()
	if err != nil {
		return fail(err)
	}
	if !cfg.LoggedIn() {
		return fail(fmt.Errorf("not logged in"))
	}

	binPath := *binary
	if binPath == "" {
		if binPath, err = os.Executable(); err != nil {
			return fail(fmt.Errorf("locate this binary: %w", err))
		}
	}

	ctx, stop := signalCtx()
	defer stop()

	// Mint a one-time enrollment token scoped to the requested tags.
	tok, err := cpclient.New(cfg.Server, cfg.Token).CreateEnrollToken(ctx, tags, 15*time.Minute)
	if err != nil {
		return fail(err)
	}

	res, err := bootstrap.Install(ctx, bootstrap.Options{
		User: user, Host: hostport,
		Password: *password, KeyPath: *keyPath, KeyPass: *keyPass,
		BinaryPath:  binPath,
		Server:      tok.Server,
		EnrollToken: tok.Token,
		AgentAddr:   *agentAddr,
		Name:        *name,
		Tags:        tags,
		Out:         os.Stdout,
	})
	if err != nil {
		return fail(err)
	}
	fmt.Printf("\nbootstrapped %s (%s)\n", *host, res.RemoteUname)
	fmt.Printf("agent installed at %s and launched.\n", res.InstallPath)
	fmt.Printf("run `openbox nodes` to see it register.\n")
	return 0
}

// --- nodes ---

func cmdNodes(args []string) int {
	fs := flag.NewFlagSet("nodes", flag.ContinueOnError)
	tag := fs.String("tag", "", "filter by tag")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.LoadClient()
	if err != nil {
		return fail(err)
	}
	if !cfg.LoggedIn() {
		return fail(fmt.Errorf("not logged in"))
	}

	ctx, stop := signalCtx()
	defer stop()

	resp, err := cpclient.New(cfg.Server, cfg.Token).ListNodes(ctx, *tag)
	if err != nil {
		return fail(err)
	}
	if len(resp.Nodes) == 0 {
		fmt.Println("no nodes yet — add one with `openbox node token`")
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tSTATUS\tOS/ARCH\tTAGS\tADDR")
	for _, n := range resp.Nodes {
		status := "offline"
		if n.Online {
			status = "online"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s/%s\t%s\t%s\n",
			n.Name, n.ID, status, n.OS, n.Arch, joinTags(n.Tags), n.Addr)
	}
	tw.Flush()
	return 0
}

func joinTags(tags []string) string {
	if len(tags) == 0 {
		return "-"
	}
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ","
		}
		out += t
	}
	return out
}
