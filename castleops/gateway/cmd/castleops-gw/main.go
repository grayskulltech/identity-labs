// castleops-gw is the reference CastleOps gateway.
//
//	castleops-gw validate  [-tools DIR] [-policy FILE]
//	castleops-gw simulate  [-tools DIR]
//	castleops-gw serve     -addr HOST:PORT -tools DIR -jwks FILE -issuer URL -audience AUD \
//	                       -tls-cert FILE -tls-key FILE -client-ca FILE [-policy FILE] [-audit FILE]
//
// serve refuses to bind anything but a loopback or RFC 1918 address unless
// -unsafe-bind is set, and refuses to run without mTLS unless -insecure-dev
// is set. Both flags are logged on every start.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/audit"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/authn"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/gateway"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/guardrail"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/mcp"
	cedarpdp "github.com/grayskulltech/identity-labs/castleops/gateway/internal/policy/cedar"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/registry"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/sim"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "validate":
		err = runValidate(os.Args[2:])
	case "simulate":
		err = runSimulate(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: castleops-gw validate|simulate|serve [flags]")
}

func loadPolicy(path string) ([]byte, error) {
	if path == "" {
		return cedarpdp.DefaultPolicies(), nil
	}
	return os.ReadFile(path)
}

func toolsDir(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	wd, _ := os.Getwd()
	return sim.DefaultToolsDir(wd)
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	tools := fs.String("tools", "", "tool definitions directory (default: locate contract/v1/tools)")
	policy := fs.String("policy", "", "Cedar policy file (default: embedded)")
	_ = fs.Parse(args)

	dir, err := toolsDir(*tools)
	if err != nil {
		return err
	}
	reg, err := registry.LoadTools(dir)
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	src, err := loadPolicy(*policy)
	if err != nil {
		return err
	}
	pdp, err := cedarpdp.New(src)
	if err != nil {
		return err
	}
	if err := pdp.Validate(); err != nil {
		return err
	}
	fmt.Printf("tools: %d loaded from %s\n", len(reg.All()), dir)
	for _, t := range reg.All() {
		fmt.Printf("  %-32s v%d  %-7s %-9s worker=%s\n", t.ID, t.Version, t.Kind, t.Domain, t.Worker)
	}
	fmt.Printf("policies: %d loaded, required forbids present\n", len(pdp.PolicyIDs()))
	for _, id := range pdp.PolicyIDs() {
		fmt.Printf("  %s\n", id)
	}
	return nil
}

func runSimulate(args []string) error {
	fs := flag.NewFlagSet("simulate", flag.ExitOnError)
	tools := fs.String("tools", "", "tool definitions directory")
	asJSON := fs.Bool("json", false, "emit audit records as JSON lines instead of a table")
	_ = fs.Parse(args)

	dir, err := toolsDir(*tools)
	if err != nil {
		return err
	}
	w, err := sim.Build(dir)
	if err != nil {
		return err
	}
	results := sim.Run(w)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, r := range w.Audit.Records() {
			_ = enc.Encode(r)
		}
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RESULT\tSCENARIO\tDECISION\tSTAGE\tPOLICIES\tGUARDRAIL")
	failed := 0
	for _, o := range results {
		mark := "pass"
		if !o.Passed {
			mark = "FAIL"
			failed++
		}
		stage := string(o.Out.DenyStage)
		if stage == "" {
			stage = "-"
		}
		pols := strings.Join(o.Out.Decision.DeterminingPolicies, ",")
		if pols == "" {
			pols = "-"
		}
		verdict := o.Out.Verdict
		if verdict == "" {
			verdict = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", mark, o.Scenario.Name, o.Out.Audit.Decision, stage, pols, verdict)
		if !o.Passed {
			fmt.Fprintf(tw, "\t  %s\t\t\t\t\n", o.Why)
		}
	}
	tw.Flush()
	fmt.Printf("\n%d scenarios, %d failed, %d audit records\n", len(results), failed, len(w.Audit.Records()))
	if failed > 0 {
		return fmt.Errorf("%d scenario(s) failed", failed)
	}
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8443", "listen address; overlay or loopback only")
	tools := fs.String("tools", "", "tool definitions directory")
	policy := fs.String("policy", "", "Cedar policy file (default: embedded)")
	jwks := fs.String("jwks", "", "JWKS file with the IdP signing keys")
	issuer := fs.String("issuer", "", "expected token issuer")
	audience := fs.String("audience", "castleops-gateway", "expected token audience")
	endpoint := fs.String("endpoint", "", "canonical MCP URL clients bind proofs to")
	tlsCert := fs.String("tls-cert", "", "server certificate")
	tlsKey := fs.String("tls-key", "", "server key")
	clientCA := fs.String("client-ca", "", "CA that issues device certificates (step-ca root)")
	auditPath := fs.String("audit", "castleops-audit.jsonl", "audit JSONL path")
	minOS := fs.String("min-os", "", "minimum attested and reported OS version")
	models := fs.String("models", "", "comma-separated allowed model identifiers")
	tz := fs.String("tz", "America/New_York", "household time zone")
	unsafeBind := fs.Bool("unsafe-bind", false, "allow binding a non-private address")
	insecureDev := fs.Bool("insecure-dev", false, "run without mTLS and accept a dev fingerprint header")
	_ = fs.Parse(args)

	if *issuer == "" || *jwks == "" || *endpoint == "" || *minOS == "" || *models == "" {
		return fmt.Errorf("serve needs -issuer, -jwks, -endpoint, -min-os, -models")
	}
	if !privateAddr(*addr) && !*unsafeBind {
		return fmt.Errorf("refusing to bind %s: not loopback or RFC 1918; pass -unsafe-bind to override", *addr)
	}
	if *unsafeBind {
		log.Printf("WARNING: -unsafe-bind set; %s is not a private address", *addr)
	}
	if (*tlsCert == "" || *tlsKey == "" || *clientCA == "") && !*insecureDev {
		return fmt.Errorf("serve needs -tls-cert, -tls-key, -client-ca unless -insecure-dev is set")
	}
	if *insecureDev {
		log.Printf("WARNING: -insecure-dev set; no mTLS, device fingerprint taken from a header")
	}

	dir, err := toolsDir(*tools)
	if err != nil {
		return err
	}
	reg, err := registry.LoadTools(dir)
	if err != nil {
		return err
	}
	src, err := loadPolicy(*policy)
	if err != nil {
		return err
	}
	pdp, err := cedarpdp.New(src)
	if err != nil {
		return err
	}
	keys, err := loadJWKS(*jwks)
	if err != nil {
		return err
	}
	sink, err := audit.OpenJSONL(*auditPath)
	if err != nil {
		return err
	}
	defer sink.Close()

	gw, err := gateway.New(gateway.Gateway{
		Tools:   reg,
		Devices: registry.NewMemoryDevices(), // persistence lands with the MDM adapter
		IdP:     &authn.TokenVerifier{Issuer: *issuer, Audience: *audience, Keys: keys},
		PDP:     pdp,
		Binding: &authn.DPoPBinding{},
		Scanner: guardrail.PatternScanner{},
		Audit:   sink,
		Workers: map[string]gateway.Worker{}, // real workers are registered by the orchestrator
		Policy: contract.PolicyParams{
			MinimumOS: *minOS, AttestationMaxAge: 7 * 24 * time.Hour, CheckinMaxAge: 24 * time.Hour,
			AllowedModels: strings.Split(*models, ","), HouseholdTimeZone: *tz,
		},
		MaxTier:  contract.TierOpaque,
		Endpoint: *endpoint,
	})
	if err != nil {
		return err
	}

	h := &mcp.Handler{Gateway: gw, AllowInsecureDevFingerprint: *insecureDev, ServerVersion: version}
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })

	srv := &http.Server{
		Addr: *addr, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second,
	}
	if *insecureDev {
		log.Printf("castleops-gw %s listening on http://%s/mcp (insecure dev)", version, *addr)
		return srv.ListenAndServe()
	}
	caPEM, err := os.ReadFile(*clientCA)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("client CA: no certificates in %s", *clientCA)
	}
	srv.TLSConfig = &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
	}
	log.Printf("castleops-gw %s listening on https://%s/mcp with mTLS", version, *addr)
	return srv.ListenAndServeTLS(*tlsCert, *tlsKey)
}

func loadJWKS(path string) (authn.StaticKeySet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Keys []authn.JWK `json:"keys"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	set := authn.StaticKeySet{}
	for _, k := range doc.Keys {
		if k.Kid == "" {
			return nil, fmt.Errorf("jwks: every key needs a kid")
		}
		pub, err := k.PublicKey()
		if err != nil {
			return nil, fmt.Errorf("jwks: %s: %w", k.Kid, err)
		}
		set[k.Kid] = pub
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("jwks: no keys")
	}
	return set, nil
}

func privateAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "localhost" {
		return host == "localhost"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || strings.HasPrefix(host, "100.") // CGNAT range used by overlays
}
