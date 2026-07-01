package main

// Onboarding subcommands: install / uninstall / status / doctor. The agent is a
// single static binary, so onboarding is the binary acting on itself plus the
// base-OS tools every systemd Linux already has (systemctl, useradd, usermod) —
// no libraries or packages to install.

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultUser     = "shadowtwin"
	defaultBinName  = "shadowtwin-agent"
	defaultBinDir   = "/usr/local/bin"
	defaultStateDir = "/var/lib/shadowtwin-agent"
	defaultUnitDir  = "/etc/systemd/system"
	defaultEnvDir   = "/etc/shadowtwin-agent"
	journalGroup    = "systemd-journal"
)

type installOpts struct {
	server, token, user                           string
	binPath, stateDir, envFile, unitPath, svcName string
}

// runInstall performs one-command onboarding: create an unprivileged user,
// install the binary, fetch the CA, write a hardened systemd unit, then enable,
// start, and verify the service.
func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	server := fs.String("server", "", "platform base URL, e.g. https://platform:8443 (required)")
	token := fs.String("token", "", "one-time enrollment token (required)")
	caSrc := fs.String("ca", "", "platform CA as a file path or URL (default: fetch <server>/ca)")
	userName := fs.String("user", defaultUser, "dedicated unprivileged service user")
	stateDir := fs.String("state-dir", defaultStateDir, "agent state directory")
	dry := fs.Bool("dry-run", false, "print the plan and the systemd unit without changing the system")
	_ = fs.Parse(args)

	if *server == "" || *token == "" {
		fatalf("install: --server and --token are required")
	}
	self, err := os.Executable()
	if err != nil {
		fatalf("install: cannot resolve own path: %v", err)
	}
	if resolved, e := filepath.EvalSymlinks(self); e == nil {
		self = resolved
	}

	o := installOpts{
		server:   strings.TrimRight(*server, "/"),
		token:    *token,
		user:     *userName,
		binPath:  filepath.Join(defaultBinDir, defaultBinName),
		stateDir: *stateDir,
		envFile:  filepath.Join(defaultEnvDir, "agent.env"),
		unitPath: filepath.Join(defaultUnitDir, defaultBinName+".service"),
		svcName:  defaultBinName,
	}
	enrollURL, ingestURL, renewURL, caURL := endpointsFromServer(o.server)

	if *dry {
		caDesc := "fetch " + caURL
		if *caSrc != "" {
			caDesc = *caSrc
		}
		fmt.Println("# install plan (dry-run — no changes made)")
		fmt.Printf("user:      create %q (system, nologin), add to %q group\n", o.user, journalGroup)
		fmt.Printf("binary:    copy %s -> %s (0755)\n", self, o.binPath)
		fmt.Printf("state dir: %s (0700, owned by %s) + certs/\n", o.stateDir, o.user)
		fmt.Printf("CA:        %s -> %s/certs/ca.crt\n", caDesc, o.stateDir)
		fmt.Printf("env file:  %s (0600)\n", o.envFile)
		fmt.Printf("unit:      %s, then systemctl enable --now %s\n", o.unitPath, o.svcName)
		fmt.Printf("endpoints: enroll=%s ingest=%s renew=%s\n", enrollURL, ingestURL, renewURL)
		fmt.Print("\n--- systemd unit ---\n", renderUnit(o))
		fmt.Print("\n--- ", o.envFile, " ---\n", envFileContent(o, enrollURL, ingestURL, renewURL))
		return
	}

	requireRoot("install")
	step := func(format string, a ...any) { fmt.Printf("==> "+format+"\n", a...) }

	step("creating service user %q", o.user)
	if err := ensureUser(o.user); err != nil {
		fatalf("install: %v", err)
	}
	if err := ensureUserInGroup(o.user, journalGroup); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add %s to %s (%v); journal reads may need manual ACLs\n", o.user, journalGroup, err)
	}
	uid, gid, err := lookupIDs(o.user)
	if err != nil {
		fatalf("install: look up %s: %v", o.user, err)
	}

	step("installing binary to %s", o.binPath)
	if err := copyExecutable(self, o.binPath); err != nil {
		fatalf("install: copy binary: %v", err)
	}

	step("creating state dir %s", o.stateDir)
	certDir := filepath.Join(o.stateDir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		fatalf("install: create state dir: %v", err)
	}

	step("fetching platform CA")
	caPEM, insecure, err := obtainCA(*caSrc, caURL)
	if err != nil {
		fatalf("install: obtain CA: %v", err)
	}
	if insecure {
		fmt.Fprintln(os.Stderr, "warning: CA fetched without verification (the CA is non-secret; pass --ca <file> out-of-band for stronger assurance)")
	}
	if err := os.WriteFile(filepath.Join(certDir, "ca.crt"), caPEM, 0o644); err != nil {
		fatalf("install: write CA: %v", err)
	}
	if err := chownTree(o.stateDir, uid, gid); err != nil {
		fatalf("install: chown state dir: %v", err)
	}

	step("writing %s", o.envFile)
	if err := writeRootFile(o.envFile, envFileContent(o, enrollURL, ingestURL, renewURL), 0o600); err != nil {
		fatalf("install: write env file: %v", err)
	}

	step("writing systemd unit %s", o.unitPath)
	if err := writeRootFile(o.unitPath, renderUnit(o), 0o644); err != nil {
		fatalf("install: write unit: %v", err)
	}

	step("enabling and starting %s", o.svcName)
	if err := run("systemctl", "daemon-reload"); err != nil {
		fatalf("install: %v", err)
	}
	if err := run("systemctl", "enable", "--now", o.svcName); err != nil {
		fatalf("install: %v", err)
	}

	step("verifying connectivity, enrollment, and service health")
	if err := verifyInstall(o, ingestURL, caPEM); err != nil {
		fmt.Fprintf(os.Stderr, "\nINSTALL VERIFICATION FAILED: %v\n\nrecent logs:\n%s\n", err, journalTail(o.svcName, 20))
		os.Exit(1)
	}

	fmt.Printf("\n✓ %s is installed and shipping. Check it with: %s status  (or: %s doctor)\n",
		defaultBinName, defaultBinName, defaultBinName)
}

// runUninstall stops and removes the service; --purge also removes state + user.
func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also remove the state directory, config, and the service user")
	userName := fs.String("user", defaultUser, "service user (used with --purge)")
	stateDir := fs.String("state-dir", defaultStateDir, "state directory (used with --purge)")
	_ = fs.Parse(args)

	requireRoot("uninstall")
	svc := defaultBinName

	_ = run("systemctl", "disable", "--now", svc) // ignore if not installed
	if err := os.Remove(filepath.Join(defaultUnitDir, svc+".service")); err == nil {
		fmt.Println("removed systemd unit")
	}
	_ = run("systemctl", "daemon-reload")
	if err := os.Remove(filepath.Join(defaultBinDir, defaultBinName)); err == nil {
		fmt.Println("removed binary")
	}

	if *purge {
		_ = os.RemoveAll(*stateDir)
		_ = os.RemoveAll(defaultEnvDir)
		fmt.Printf("removed state dir %s and config %s\n", *stateDir, defaultEnvDir)
		if userExists(*userName) && commandExists("userdel") {
			if err := run("userdel", *userName); err == nil {
				fmt.Printf("removed user %s\n", *userName)
			}
		}
	} else {
		fmt.Printf("kept state dir %s and config %s (use --purge to remove)\n", *stateDir, defaultEnvDir)
	}
	fmt.Println("✓ uninstalled")
}

// runStatus prints a one-glance summary of the installed agent.
func runStatus(args []string) {
	_ = flag.NewFlagSet("status", flag.ExitOnError).Parse(args)
	svc := defaultBinName

	fmt.Printf("binary:    %s\n", presence(filepath.Join(defaultBinDir, defaultBinName)))
	fmt.Printf("service:   active=%s enabled=%s\n", cmdOut("systemctl", "is-active", svc), cmdOut("systemctl", "is-enabled", svc))

	env, _ := parseEnvFile(filepath.Join(defaultEnvDir, "agent.env"))
	stateDir := envOr(env, "HOST_AGENT_STATE_DIR", defaultStateDir)

	if na, err := certNotAfter(filepath.Join(stateDir, "certs", "client.crt")); err == nil {
		fmt.Printf("cert:      valid until %s (%s left)\n", na.UTC().Format(time.RFC3339), time.Until(na).Round(time.Hour))
	} else {
		fmt.Printf("cert:      not enrolled\n")
	}
	fmt.Printf("endpoint:  %s\n", envOr(env, "INGEST_ENDPOINT", "(unset)"))
	fmt.Printf("queue:     %d event(s) buffered\n", queueDepth(stateDir))
}

// runDoctor runs diagnostic checks and exits non-zero if any FAIL.
func runDoctor(args []string) {
	_ = flag.NewFlagSet("doctor", flag.ExitOnError).Parse(args)
	svc := defaultBinName
	fails := 0
	check := func(name string, ok bool, detail string) {
		mark := "PASS"
		if !ok {
			mark = "FAIL"
			fails++
		}
		fmt.Printf("[%s] %s%s\n", mark, name, suffix(detail))
	}
	warn := func(name, detail string) { fmt.Printf("[WARN] %s%s\n", name, suffix(detail)) }

	binPath := filepath.Join(defaultBinDir, defaultBinName)
	check("binary installed ("+binPath+")", fileExists(binPath), "")

	uOK := userExists(defaultUser)
	check("service user "+defaultUser+" exists", uOK, "")
	if uOK {
		check(defaultUser+" in "+journalGroup+" group", userInGroup(defaultUser, journalGroup), "")
	}

	env, envErr := parseEnvFile(filepath.Join(defaultEnvDir, "agent.env"))
	check("config present ("+filepath.Join(defaultEnvDir, "agent.env")+")", envErr == nil, errStr(envErr))
	stateDir := envOr(env, "HOST_AGENT_STATE_DIR", defaultStateDir)

	certPath := filepath.Join(stateDir, "certs", "client.crt")
	if na, err := certNotAfter(certPath); err == nil {
		switch {
		case time.Until(na) <= 0:
			check("client certificate not expired", false, "expired "+na.UTC().Format(time.RFC3339))
		case time.Until(na) < 7*24*time.Hour:
			warn("client certificate near expiry", na.UTC().Format(time.RFC3339))
		default:
			check("client certificate valid", true, "until "+na.UTC().Format(time.RFC3339))
		}
	} else {
		check("client certificate present", false, errStr(err))
	}

	if ingest := env["INGEST_ENDPOINT"]; ingest != "" {
		caPEM, _ := os.ReadFile(filepath.Join(stateDir, "certs", "ca.crt"))
		err := probeTLS(ingest, caPEM)
		check("platform reachable ("+ingest+")", err == nil, errStr(err))
	} else {
		warn("platform endpoint unknown", "no INGEST_ENDPOINT — run install")
	}

	if commandExists("systemctl") {
		check("service active", isActive(svc), "")
	} else {
		warn("systemctl not found", "not a systemd host?")
	}

	fmt.Printf("[INFO] buffered events: %d\n", queueDepth(stateDir))

	if fails > 0 {
		fmt.Printf("\n%d check(s) failed\n", fails)
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}

// --- rendering (pure; unit-tested) ---

func endpointsFromServer(base string) (enrollURL, ingestURL, renewURL, caURL string) {
	base = strings.TrimRight(base, "/")
	return base + "/enroll", base + "/ingest", base + "/renew", base + "/ca"
}

func renderUnit(o installOpts) string {
	return fmt.Sprintf(`[Unit]
Description=ShadowTwin host agent
Documentation=https://github.com/Aakhri-Pastaa/ShadowTwin
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
EnvironmentFile=%s
ExecStart=%s
Restart=on-failure
RestartSec=5

# Hardening: the agent only reads the journal and dials out over TLS.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ProtectClock=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictSUIDSGID=true
SystemCallArchitectures=native
CapabilityBoundingSet=
AmbientCapabilities=
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, o.user, o.user, o.envFile, o.binPath, o.stateDir)
}

func envFileContent(o installOpts, enrollURL, ingestURL, renewURL string) string {
	return fmt.Sprintf(`# Managed by 'shadowtwin-agent install'. Contains the enrollment token; keep mode 0600.
HOST_AGENT_STATE_DIR=%s
INGEST_ENDPOINT=%s
AGENT_ENROLL_ENDPOINT=%s
AGENT_RENEW_ENDPOINT=%s
AGENT_ENROLL_TOKEN=%s
`, o.stateDir, ingestURL, enrollURL, renewURL, o.token)
}

// --- system helpers ---

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "shadowtwin-agent: "+format+"\n", a...)
	os.Exit(1)
}

func requireRoot(cmd string) {
	if os.Geteuid() != 0 {
		fatalf("%s must run as root (try: sudo)", cmd)
	}
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cmdOut(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return "unknown"
}

func commandExists(name string) bool { _, err := exec.LookPath(name); return err == nil }
func fileExists(p string) bool       { _, err := os.Stat(p); return err == nil }
func userExists(name string) bool    { _, err := user.Lookup(name); return err == nil }

func nologinShell() string {
	for _, p := range []string{"/usr/sbin/nologin", "/sbin/nologin", "/bin/false"} {
		if fileExists(p) {
			return p
		}
	}
	return "/bin/false"
}

func ensureUser(name string) error {
	if userExists(name) {
		return nil
	}
	if !commandExists("useradd") {
		return fmt.Errorf("useradd not found; create the %q system user manually", name)
	}
	return run("useradd", "--system", "--no-create-home", "--shell", nologinShell(), name)
}

func ensureUserInGroup(userName, group string) error {
	if _, err := user.LookupGroup(group); err != nil {
		return fmt.Errorf("group %q not found: %w", group, err)
	}
	if !commandExists("usermod") {
		return fmt.Errorf("usermod not found")
	}
	return run("usermod", "-aG", group, userName)
}

func userInGroup(userName, group string) bool {
	u, err := user.Lookup(userName)
	if err != nil {
		return false
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return false
	}
	ids, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, id := range ids {
		if id == g.Gid {
			return true
		}
	}
	return false
}

func lookupIDs(name string) (uid, gid int, err error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, _ = strconv.Atoi(u.Uid)
	gid, _ = strconv.Atoi(u.Gid)
	return uid, gid, nil
}

func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func writeRootFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(p, uid, gid)
	})
}

func obtainCA(caSrc, caURL string) (pemBytes []byte, insecureFetched bool, err error) {
	switch {
	case caSrc == "":
		b, e := fetchCAInsecure(caURL)
		return b, true, e
	case strings.HasPrefix(caSrc, "http://"), strings.HasPrefix(caSrc, "https://"):
		b, e := fetchCAInsecure(caSrc)
		return b, true, e
	default:
		b, e := os.ReadFile(caSrc)
		return b, false, e
	}
}

func fetchCAInsecure(rawURL string) ([]byte, error) {
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}, //nolint:gosec // CA is non-secret; pinned after fetch
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if blk, _ := pem.Decode(b); blk == nil {
		return nil, fmt.Errorf("response from %s is not a PEM certificate", rawURL)
	}
	return b, nil
}

func certNotAfter(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return time.Time{}, fmt.Errorf("no PEM block in %s", path)
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m, sc.Err()
}

func queueDepth(stateDir string) int {
	entries, err := os.ReadDir(filepath.Join(stateDir, "queue"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

func probeTLS(endpoint string, caPEM []byte) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	conf := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: u.Hostname()}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return fmt.Errorf("invalid CA PEM")
		}
		conf.RootCAs = pool
	} else {
		conf.InsecureSkipVerify = true //nolint:gosec // reachability probe only
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host, conf)
	if err != nil {
		return err
	}
	return conn.Close()
}

func verifyInstall(o installOpts, ingestURL string, caPEM []byte) error {
	if err := probeTLS(ingestURL, caPEM); err != nil {
		return fmt.Errorf("cannot reach platform at %s: %w", ingestURL, err)
	}
	certPath := filepath.Join(o.stateDir, "certs", "client.crt")
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if isActive(o.svcName) && fileExists(certPath) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("service did not become active and enrolled within 25s")
}

func isActive(svc string) bool {
	out, _ := exec.Command("systemctl", "is-active", svc).Output()
	return strings.TrimSpace(string(out)) == "active"
}

func journalTail(svc string, n int) string {
	out, _ := exec.Command("journalctl", "-u", svc, "-n", strconv.Itoa(n), "--no-pager").CombinedOutput()
	if len(out) == 0 {
		return "(no logs)"
	}
	return string(out)
}

func presence(path string) string {
	if fileExists(path) {
		return path + " (present)"
	}
	return path + " (missing)"
}

func envOr(m map[string]string, key, def string) string {
	if v := m[key]; v != "" {
		return v
	}
	return def
}

func suffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " — " + detail
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
