package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"encoding/json"

	"github.com/armarquez/project-cypher/internal/agents"
	"github.com/armarquez/project-cypher/internal/architect"
	"github.com/armarquez/project-cypher/internal/config"
	"github.com/armarquez/project-cypher/internal/doctor"
	"github.com/armarquez/project-cypher/internal/gateway"
	"github.com/armarquez/project-cypher/internal/github"
	"github.com/armarquez/project-cypher/internal/orchestrator"
	"github.com/armarquez/project-cypher/internal/secrets"
	"github.com/armarquez/project-cypher/internal/session"
	"github.com/armarquez/project-cypher/internal/setup"
	"github.com/armarquez/project-cypher/internal/skills"
	"github.com/armarquez/project-cypher/internal/validate"
)

const version = "0.1.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor":
			runDoctor(os.Args[2:])
			return
		case "setup":
			runSetup(os.Args[2:])
			return
		case "validate":
			runValidate(os.Args[2:])
			return
		case "version":
			fmt.Printf("cypher %s\n", version)
			return
		}
	}
	runOrchestrator()
}

func runDoctor(args []string) {
	fs := flag.NewFlagSet("cypher doctor", flag.ExitOnError)
	cfgPath := fs.String("config", envOrDefault("CYPHER_CONFIG", "configs/project-cypher.yaml"), "project config YAML")
	skillsDir := fs.String("skills-dir", "skills", "directory containing skill bundle YAML files")
	fs.Parse(args) //nolint:errcheck

	// Best-effort: load config to get owner/repo for per-repo token lookup.
	var ghOwner, ghRepo string
	if c, err := config.Load(*cfgPath); err == nil {
		ghOwner, ghRepo, _ = config.ParseRepo(c.TargetRepo)
	}

	cfg := doctor.Config{
		Owner:        ghOwner,
		Repo:         ghRepo,
		ConfigPath:   *cfgPath,
		SkillsDir:    *skillsDir,
		DockerSocket: envOrDefault("CYPHER_DOCKER_SOCKET", "/var/run/docker.sock"),
		OpenHandsURL: envOrDefault("CYPHER_OPENHANDS_URL", "http://localhost:3000"),
		WorkerImage:  envOrDefault("CYPHER_WORKER_IMAGE", "ghcr.io/all-hands-ai/openhands:main"),
	}

	fmt.Println("Checking environment...")
	fmt.Println()

	results := doctor.Run(context.Background(), cfg)
	failed, warned := 0, 0
	for _, r := range results {
		switch {
		case r.Pass:
			if r.Detail != "" {
				fmt.Printf("  ✓ %s (%s)\n", r.Name, r.Detail)
			} else {
				fmt.Printf("  ✓ %s\n", r.Name)
			}
		case r.Warn:
			fmt.Printf("  ⚠ %s\n", r.Name)
			if r.Fix != "" {
				fmt.Printf("    → %s\n", r.Fix)
			}
			warned++
		default:
			fmt.Printf("  ✗ %s\n", r.Name)
			if r.Fix != "" {
				fmt.Printf("    → %s\n", r.Fix)
			}
			failed++
		}
	}
	fmt.Println()

	if failed > 0 {
		fmt.Printf("%d check(s) failed.\n", failed)
		os.Exit(1)
	}
	if warned > 0 {
		fmt.Printf("%d warning(s).\n", warned)
	} else {
		fmt.Println("All checks passed.")
	}
}

func runSetup(args []string) {
	fs := flag.NewFlagSet("cypher setup", flag.ExitOnError)
	cfgPath := fs.String("config", envOrDefault("CYPHER_CONFIG", "configs/project-cypher.yaml"), "project config YAML")
	envPath := fs.String("env", ".env", "path to .env file to write credentials into")
	cypherDir := fs.String("cypher-dir", ".cypher", "directory for Cypher runtime artifacts")
	appName := fs.String("app-name", "", `GitHub App name (default: "cypher-{owner}-{repo}"); use when the default name is already taken`)
	pemStorage := fs.String("pem-storage", "", `where to store the GitHub App private key: "file" or "1password" (default: prompt interactively)`)
	opVault := fs.String("op-vault", "Private", `1Password vault name (used when --pem-storage=1password)`)
	dryRun := fs.Bool("dry-run", false, "validate vault / filesystem access without creating a GitHub App or writing .env")
	fs.Parse(args) //nolint:errcheck

	setupCfg := setup.Config{
		EnvPath:    *envPath,
		CypherDir:  *cypherDir,
		AppName:    *appName,
		PEMStorage: *pemStorage,
		OPVault:    *opVault,
	}

	if *dryRun {
		if err := setup.DryRun(context.Background(), setupCfg); err != nil {
			fmt.Fprintf(os.Stderr, "dry-run failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	setupCfg.TargetRepo = cfg.TargetRepo

	if err := setup.Run(context.Background(), setupCfg); err != nil {
		fmt.Fprintf(os.Stderr, "setup failed: %v\n", err)
		os.Exit(1)
	}
}

func runValidate(args []string) {
	fs := flag.NewFlagSet("cypher validate", flag.ExitOnError)
	cfgPath := fs.String("config", envOrDefault("CYPHER_CONFIG", "configs/project-cypher.yaml"), "project config YAML")
	skillsDir := fs.String("skills-dir", "skills", "directory containing skill bundle YAML files")
	fs.Parse(args) //nolint:errcheck

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: validation failed\n\n  %v\n\n1 error found.\n", *cfgPath, err)
		os.Exit(1)
	}

	issues := validate.Check(cfg, *skillsDir)
	if len(issues) == 0 {
		fmt.Printf("%s: ok\n", *cfgPath)
		return
	}

	fmt.Fprintf(os.Stderr, "%s: validation issues found\n\n", *cfgPath)
	errors, warnings := 0, 0
	for _, issue := range issues {
		symbol := "✗"
		if issue.Severity == validate.Warning {
			symbol = "⚠"
			warnings++
		} else {
			errors++
		}
		if issue.Got != "" {
			fmt.Fprintf(os.Stderr, "  %s: %q\n", issue.Field, issue.Got)
		} else {
			fmt.Fprintf(os.Stderr, "  %s\n", issue.Field)
		}
		fmt.Fprintf(os.Stderr, "    %s %s\n", symbol, issue.Message)
		if issue.Hint != "" {
			fmt.Fprintf(os.Stderr, "    → %s\n", issue.Hint)
		}
		fmt.Fprintln(os.Stderr)
	}

	var summary []string
	if errors > 0 {
		summary = append(summary, fmt.Sprintf("%d error(s)", errors))
	}
	if warnings > 0 {
		summary = append(summary, fmt.Sprintf("%d warning(s)", warnings))
	}
	fmt.Fprintln(os.Stderr, strings.Join(summary, ", ")+".")

	if errors > 0 {
		os.Exit(1)
	}
}

func runOrchestrator() {
	var (
		cfgPath     = flag.String("config", envOrDefault("CYPHER_CONFIG", "configs/project-cypher.yaml"), "project config YAML")
		skillsDir   = flag.String("skills-dir", "skills", "directory containing skill bundle YAML files")
		loop        = flag.Bool("loop", false, "run continuously (default: run once and exit)")
		pollSecs    = flag.Int("poll", 30, "seconds between issue polls when --loop is set")
		gatewayAddr = flag.String("gateway-addr", ":8080", "Control Plane gateway listen address")
	)
	flag.Parse()

	log := slog.Default()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	owner, repo, err := config.ParseRepo(cfg.TargetRepo)
	if err != nil {
		log.Error("parse target_repo", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	ghClient, err := buildGHClient(ctx, owner, repo, log)
	if err != nil {
		log.Error("build GitHub client", "err", err)
		os.Exit(1)
	}

	credKeys, err := loadResolvedCredentials(ctx)
	if err != nil {
		log.Error("resolve gateway credentials", "err", err)
		os.Exit(1)
	}
	creds := gateway.NewCredentialStore(credKeys)
	router := gateway.NewRouter(http.DefaultClient, nil, creds)

	var webhookSrv *gateway.WebhookServer
	webhookSecret, err := secrets.ResolveEnv(ctx, "CYPHER_GH_WEBHOOK_SECRET")
	if err != nil {
		log.Error("resolve webhook secret", "err", err)
		os.Exit(1)
	}
	if webhookSecret != "" {
		webhookSrv = gateway.NewWebhookServer(webhookSecret)
	}
	gw := gateway.NewServer(*gatewayAddr, router, webhookSrv)
	go func() {
		if err := gw.Start(); err != nil {
			log.Error("gateway error", "err", err)
		}
	}()


	ohURL := envOrDefault("CYPHER_OPENHANDS_URL", "http://localhost:3000")
	socketPath := envOrDefault("CYPHER_DOCKER_SOCKET", "/var/run/docker.sock")

	containerCfg := session.ContainerConfig{
		Image:       envOrDefault("CYPHER_WORKER_IMAGE", "ghcr.io/all-hands-ai/openhands:main"),
		Env:         []string{"OPENAI_BASE_URL=http://host.docker.internal:8080"},
		NetworkMode: "bridge",
	}
	mgr := session.NewManager(socketPath, containerCfg)

	orch := orchestrator.New(
		owner, repo,
		cfg,
		ghClient,
		&orchestrator.ManagerAdapter{M: mgr},
		func() orchestrator.OHClient {
			return session.NewOpenHandsClient(http.DefaultClient, ohURL)
		},
		time.Duration(*pollSecs)*time.Second,
		log,
	)

	bundles, err := skills.LoadDir(*skillsDir)
	if err != nil {
		log.Error("load skill bundles", "dir", *skillsDir, "err", err)
		os.Exit(1)
	}
	orch.WithBundles(bundles)
	log.Info("skill bundles loaded", "dir", *skillsDir, "count", len(bundles))

	// Wire Architect-tier agents based on active guardrails.
	archClient, err := architectClient(ctx, cfg)
	if err != nil {
		log.Error("resolve architect API key", "err", err)
		os.Exit(1)
	}
	if archClient == nil {
		ossEnabled := len(cfg.Guardrails) == 0 || cfg.GuardrailEnabled("oss_adoption:evaluate")
		if ossEnabled {
			log.Warn("ANTHROPIC_API_KEY not set — oss_adoption:evaluate guardrail inactive")
		}
		if len(activeDocChecks(cfg)) > 0 {
			log.Warn("ANTHROPIC_API_KEY not set — docs:* guardrails inactive")
		}
		if len(cfg.Guardrails) == 0 || cfg.GuardrailEnabled("security:consistency-review") {
			log.Warn("ANTHROPIC_API_KEY not set — security:consistency-review guardrail inactive")
		}
	} else {
		ossEnabled := len(cfg.Guardrails) == 0 || cfg.GuardrailEnabled("oss_adoption:evaluate")
		if ossEnabled {
			orch.WithOSSEvaluator(agents.NewOSSEvaluator(archClient))
			log.Info("oss_adoption:evaluate active — OSS evaluator wired")
		}
		if webhookSrv != nil {
			docChecks := activeDocChecks(cfg)
			secEnabled := len(cfg.Guardrails) == 0 || cfg.GuardrailEnabled("security:consistency-review")

			var docAgent *agents.DocumentationAgent
			if len(docChecks) > 0 {
				docAgent = agents.NewDocumentationAgent(archClient, ghClient)
				log.Info("docs guardrails active — Documentation Agent wired for PR webhook", "checks", docChecks)
			}
			var secAgent *agents.SecurityReviewer
			if secEnabled {
				secAgent = agents.NewSecurityReviewer(archClient, ghClient)
				log.Info("security:consistency-review active — Security Reviewer wired for PR webhook")
			}
			if docAgent != nil || secAgent != nil {
				webhookSrv.Register("pull_request", makePRReviewHandler(docAgent, secAgent, owner, repo, docChecks, log))
			}
		}
	}

	if *loop {
		log.Info("starting orchestrator loop", "owner", owner, "repo", repo, "poll", *pollSecs)
		for {
			err := orch.RunOnce(ctx)
			if errors.Is(err, orchestrator.ErrNoWork) {
				log.Info("queue empty — no workable cypher issues, stopping")
				break
			}
			if err != nil {
				log.Error("run once failed", "err", err)
			}
			time.Sleep(time.Duration(*pollSecs) * time.Second)
		}
	} else {
		log.Info("running orchestrator once", "owner", owner, "repo", repo)
		if err := orch.RunOnce(ctx); err != nil && !errors.Is(err, orchestrator.ErrNoWork) {
			log.Error("run failed", "err", err)
			os.Exit(1)
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// buildGHClient constructs the GitHub client used by the orchestrator.
// It prefers App-based auth (CYPHER_GH_APP_ID + CYPHER_GH_INSTALLATION_ID +
// CYPHER_GH_APP_PRIVATE_KEY) and falls back to a PAT (CYPHER_GH_TOKEN_*) when
// any App credential is absent — useful for local testing without a registered App.
func buildGHClient(ctx context.Context, owner, repo string, log *slog.Logger) (*github.Client, error) {
	appIDStr := os.Getenv("CYPHER_GH_APP_ID")
	installIDStr := os.Getenv("CYPHER_GH_INSTALLATION_ID")
	pemRaw, err := secrets.ResolveEnv(ctx, "CYPHER_GH_APP_PRIVATE_KEY")
	if err != nil {
		return nil, fmt.Errorf("resolve CYPHER_GH_APP_PRIVATE_KEY: %w", err)
	}

	if appIDStr != "" && installIDStr != "" && pemRaw != "" {
		appID, err := strconv.ParseInt(appIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse CYPHER_GH_APP_ID: %w", err)
		}
		installID, err := strconv.ParseInt(installIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse CYPHER_GH_INSTALLATION_ID: %w", err)
		}
		log.Info("authenticating as GitHub App", "app_id", appID, "installation_id", installID)
		return github.NewClientFromApp(appID, installID, []byte(pemRaw), http.DefaultClient, "")
	}

	// Fall back to PAT (CYPHER_GH_TOKEN_{OWNER}_{REPO} or CYPHER_GH_TOKEN).
	rawToken, tokenVar, _ := config.ResolveGHToken(owner, repo)
	if rawToken == "" {
		return nil, fmt.Errorf("no GitHub credentials found — set CYPHER_GH_APP_ID/CYPHER_GH_INSTALLATION_ID/CYPHER_GH_APP_PRIVATE_KEY (via just setup) or %s in your .env file", tokenVar)
	}
	token, err := secrets.Resolve(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", tokenVar, err)
	}
	log.Warn("authenticating with PAT — consider running just setup to use GitHub App auth", "var", tokenVar)
	return github.NewClient(token, http.DefaultClient, ""), nil
}

// architectClient resolves ANTHROPIC_API_KEY through the secrets chain and
// returns an *architect.Client. Returns nil (no error) when the key is absent —
// callers are responsible for warning when active guardrails depend on it.
func architectClient(ctx context.Context, cfg *config.Config) (*architect.Client, error) {
	apiKey, err := secrets.ResolveEnv(ctx, "ANTHROPIC_API_KEY")
	if err != nil {
		return nil, fmt.Errorf("resolve ANTHROPIC_API_KEY: %w", err)
	}
	if apiKey == "" {
		return nil, nil
	}
	model := strings.TrimPrefix(cfg.ArchitectModel, "anthropic/")
	return architect.New(model, apiKey, nil), nil
}

// loadResolvedCredentials reads vendor API keys from environment variables,
// resolving any op:// references through the secrets chain.
func loadResolvedCredentials(ctx context.Context) (map[gateway.Vendor]string, error) {
	pairs := []struct {
		vendor gateway.Vendor
		envVar string
	}{
		{gateway.VendorGemini, "GEMINI_API_KEY"},
		{gateway.VendorAnthropic, "ANTHROPIC_API_KEY"},
		{gateway.VendorOpenAI, "OPENAI_API_KEY"},
	}
	keys := make(map[gateway.Vendor]string)
	for _, p := range pairs {
		v, err := secrets.ResolveEnv(ctx, p.envVar)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", p.envVar, err)
		}
		if v != "" {
			keys[p.vendor] = v
		}
	}
	return keys, nil
}

// activeDocChecks returns the docs:* guardrail IDs that are active in cfg.
func activeDocChecks(cfg *config.Config) []string {
	ids := []string{"docs:require-readme-update", "docs:require-arch-doc-update"}
	if len(cfg.Guardrails) == 0 {
		return ids // empty list means all guardrails active
	}
	var active []string
	for _, id := range ids {
		if cfg.GuardrailEnabled(id) {
			active = append(active, id)
		}
	}
	return active
}

// prWebhookPayload is the subset of a GitHub pull_request event we need.
type prWebhookPayload struct {
	Number      int    `json:"number"`
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pull_request"`
}

// makePRReviewHandler returns a gateway.Handler that fires the Documentation
// Agent and SecurityReviewer on opened/synchronize/reopened pull_request events.
// Both agents run sequentially; errors from either are logged and returned.
func makePRReviewHandler(
	docAgent *agents.DocumentationAgent,
	secAgent *agents.SecurityReviewer,
	owner, repo string,
	activeDocChecks []string,
	log *slog.Logger,
) gateway.Handler {
	return func(ctx context.Context, event gateway.Event) error {
		switch event.Action {
		case "opened", "synchronize", "reopened":
		default:
			return nil // ignore closed, labeled, etc.
		}
		var payload prWebhookPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("parse PR webhook payload: %w", err)
		}
		prNumber := payload.PullRequest.Number
		if prNumber == 0 {
			prNumber = payload.Number
		}
		if prNumber == 0 {
			return fmt.Errorf("could not determine PR number from webhook payload")
		}

		var retErr error

		if docAgent != nil {
			log.Info("documentation agent reviewing PR", "pr", prNumber, "action", event.Action)
			if err := docAgent.Run(ctx, owner, repo, prNumber, activeDocChecks); err != nil {
				log.Error("documentation agent failed", "pr", prNumber, "err", err)
				retErr = err
			}
		}

		if secAgent != nil {
			log.Info("security reviewer reviewing PR", "pr", prNumber, "action", event.Action)
			if err := secAgent.Run(ctx, owner, repo, prNumber); err != nil {
				log.Error("security reviewer failed", "pr", prNumber, "err", err)
				retErr = err
			}
		}

		return retErr
	}
}
