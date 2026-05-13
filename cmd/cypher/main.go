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

	"github.com/armarquez/project-cypher/internal/config"
	"github.com/armarquez/project-cypher/internal/doctor"
	"github.com/armarquez/project-cypher/internal/gateway"
	"github.com/armarquez/project-cypher/internal/github"
	"github.com/armarquez/project-cypher/internal/orchestrator"
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
	fs.Parse(args) //nolint:errcheck

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	if err := setup.Run(context.Background(), setup.Config{
		TargetRepo: cfg.TargetRepo,
		EnvPath:    *envPath,
		CypherDir:  *cypherDir,
	}); err != nil {
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

	token, tokenVar, _ := config.ResolveGHToken(owner, repo)
	if token == "" {
		log.Error("GitHub token not set",
			"tried", tokenVar,
			"fallback", "CYPHER_GH_TOKEN",
			"hint", "set "+tokenVar+" in your .env file",
		)
		os.Exit(1)
	}

	creds := gateway.LoadCredentials()
	router := gateway.NewRouter(http.DefaultClient, nil, creds)
	gw := gateway.NewServer(*gatewayAddr, router)
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
