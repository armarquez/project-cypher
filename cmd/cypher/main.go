package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
	failed := 0
	for _, r := range results {
		if r.Pass {
			if r.Detail != "" {
				fmt.Printf("  ✓ %s (%s)\n", r.Name, r.Detail)
			} else {
				fmt.Printf("  ✓ %s\n", r.Name)
			}
		} else {
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
	fmt.Println("All checks passed.")
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

	rawToken, tokenVar, _ := config.ResolveGHToken(owner, repo)
	if rawToken == "" {
		log.Error("GitHub token not set",
			"tried", tokenVar,
			"fallback", "CYPHER_GH_TOKEN",
			"hint", "set "+tokenVar+" in your .env file",
		)
		os.Exit(1)
	}
	token, err := secrets.Resolve(context.Background(), rawToken)
	if err != nil {
		log.Error("resolve GitHub token", "var", tokenVar, "err", err)
		os.Exit(1)
	}

	creds := gateway.LoadCredentials()
	router := gateway.NewRouter(http.DefaultClient, nil, creds)

	var webhookSrv *gateway.WebhookServer
	if secret := os.Getenv("CYPHER_GH_WEBHOOK_SECRET"); secret != "" {
		webhookSrv = gateway.NewWebhookServer(secret)
	}
	gw := gateway.NewServer(*gatewayAddr, router, webhookSrv)
	go func() {
		if err := gw.Start(); err != nil {
			log.Error("gateway error", "err", err)
		}
	}()

	ghClient := github.NewClient(token, http.DefaultClient, "")

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

	// Wire Architect-tier agents based on active guardrails.
	archClient := architectClient(cfg)
	if archClient != nil {
		ossEnabled := len(cfg.Guardrails) == 0 || cfg.GuardrailEnabled("oss_adoption:evaluate")
		if ossEnabled {
			orch.WithOSSEvaluator(agents.NewOSSEvaluator(archClient))
			log.Info("oss_adoption:evaluate active — OSS evaluator wired")
		}
		if webhookSrv != nil {
			docChecks := activeDocChecks(cfg)
			if len(docChecks) > 0 {
				docAgent := agents.NewDocumentationAgent(archClient, ghClient)
				webhookSrv.Register("pull_request", makePRReviewHandler(docAgent, owner, repo, docChecks, log))
				log.Info("docs guardrails active — Documentation Agent registered for PR webhook", "checks", docChecks)
			}
		}
	}

	ctx := context.Background()
	if *loop {
		log.Info("starting orchestrator loop", "owner", owner, "repo", repo, "poll", *pollSecs)
		for {
			if err := orch.RunOnce(ctx); err != nil {
				log.Error("run once failed", "err", err)
			}
			time.Sleep(time.Duration(*pollSecs) * time.Second)
		}
	} else {
		log.Info("running orchestrator once", "owner", owner, "repo", repo)
		if err := orch.RunOnce(ctx); err != nil {
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

// architectClient returns an *architect.Client if ANTHROPIC_API_KEY is set
// and the config specifies an anthropic/ model. Returns nil otherwise.
func architectClient(cfg *config.Config) *architect.Client {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil
	}
	model := strings.TrimPrefix(cfg.ArchitectModel, "anthropic/")
	return architect.New(model, apiKey, nil)
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
// Agent on opened/synchronize/reopened pull_request events.
func makePRReviewHandler(
	agent *agents.DocumentationAgent,
	owner, repo string,
	activeChecks []string,
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
		log.Info("documentation agent reviewing PR", "pr", prNumber, "action", event.Action)
		if err := agent.Run(ctx, owner, repo, prNumber, activeChecks); err != nil {
			log.Error("documentation agent failed", "pr", prNumber, "err", err)
			return err
		}
		return nil
	}
}
