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
	"github.com/armarquez/project-cypher/internal/gateway"
	"github.com/armarquez/project-cypher/internal/github"
	"github.com/armarquez/project-cypher/internal/orchestrator"
	"github.com/armarquez/project-cypher/internal/session"
)

const version = "0.1.0"

func main() {
	var (
		cfgPath    = flag.String("config", envOrDefault("CYPHER_CONFIG", "configs/project-cypher.yaml"), "project config YAML")
		loop       = flag.Bool("loop", false, "run continuously (default: run once and exit)")
		pollSecs   = flag.Int("poll", 30, "seconds between issue polls when --loop is set")
		gatewayAddr = flag.String("gateway-addr", ":8080", "Control Plane gateway listen address")
	)
	flag.Parse()

	log := slog.Default()

	if len(flag.Args()) > 0 && flag.Args()[0] == "version" {
		fmt.Printf("cypher %s\n", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	token := os.Getenv("CYPHER_GITHUB_TOKEN")
	if token == "" {
		log.Error("CYPHER_GITHUB_TOKEN is required")
		os.Exit(1)
	}

	owner, repo, err := parseRepo(cfg.TargetRepo)
	if err != nil {
		log.Error("parse target_repo", "err", err)
		os.Exit(1)
	}

	// Start the Control Plane gateway (LLM proxy with credential injection).
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

func parseRepo(targetRepo string) (owner, repo string, err error) {
	// Accepts https://github.com/owner/repo or owner/repo.
	s := strings.TrimPrefix(targetRepo, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimSuffix(s, ".git")
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse %q as owner/repo", targetRepo)
	}
	return parts[0], parts[1], nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
