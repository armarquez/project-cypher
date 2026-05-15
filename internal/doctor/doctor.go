package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/armarquez/project-cypher/internal/config"
	"github.com/armarquez/project-cypher/internal/secrets"
	"github.com/armarquez/project-cypher/internal/validate"
)

// Result is the outcome of a single doctor check.
type Result struct {
	Name   string
	Pass   bool
	Detail string // shown on pass: e.g. "user: armarquez"
	Fix    string // shown on fail: actionable remediation hint
}

// Config holds all inputs the doctor needs to run its checks.
// Owner and Repo are derived from a successfully loaded target_repo;
// they may be empty if the config file is unreadable, in which case
// token resolution falls back to CYPHER_GH_TOKEN only.
type Config struct {
	Owner        string
	Repo         string
	ConfigPath   string
	SkillsDir    string
	DockerSocket string
	OpenHandsURL string
	WorkerImage  string
	// OpPath is the path to the `op` CLI binary used for CheckSecrets.
	// Defaults to "op" from PATH when empty.
	OpPath string
}

// knownSecretVars is the set of env vars the doctor scans for op:// references.
var knownSecretVars = []string{
	"CYPHER_GH_TOKEN",
	"CYPHER_GH_APP_PRIVATE_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
}

// Run executes all environment checks and returns their results in order.
func Run(ctx context.Context, cfg Config) []Result {
	var results []Result

	token, varName, deprecated := config.ResolveGHToken(cfg.Owner, cfg.Repo)
	results = append(results, CheckTokenSet(token, varName, deprecated))

	ghClient := &http.Client{Timeout: 10 * time.Second}
	results = append(results, CheckGitHub(ctx, ghClient, "https://api.github.com", token, varName)...)

	results = append(results, CheckConfig(cfg.ConfigPath, cfg.SkillsDir)...)

	dockerClient := newUnixClient(cfg.DockerSocket)
	results = append(results, CheckDocker(ctx, dockerClient, "http://docker", cfg.WorkerImage)...)

	ohClient := &http.Client{Timeout: 10 * time.Second}
	results = append(results, CheckOpenHands(ctx, ohClient, cfg.OpenHandsURL))

	// Build the full set of secret vars to scan, including the per-repo token var.
	secretVars := append([]string(nil), knownSecretVars...)
	if varName != "CYPHER_GH_TOKEN" {
		secretVars = append(secretVars, varName)
	}
	op := &secrets.OnePassword{OpPath: cfg.OpPath}
	results = append(results, CheckSecrets(ctx, secretVars, op)...)

	results = append(results, CheckPEMFile()...)

	return results
}

func newUnixClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// CheckTokenSet checks that the resolved GitHub token env var is non-empty.
// varName is the specific env var being checked (shown in output).
// deprecated is reserved for future use and currently always false.
func CheckTokenSet(token, varName string, deprecated bool) Result {
	if token == "" {
		return Result{
			Name: varName + " set",
			Pass: false,
			Fix:  fmt.Sprintf("set %s (or CYPHER_GH_TOKEN as a global fallback) in your .env file or shell", varName),
		}
	}
	return Result{Name: varName + " set", Pass: true}
}

// CheckGitHub validates the token against the GitHub API and inspects its scopes.
// varName is included in failure messages so the user knows which var to fix.
func CheckGitHub(ctx context.Context, client *http.Client, apiBase, token, varName string) []Result {
	if token == "" {
		return []Result{
			{Name: "GitHub token valid", Pass: false, Fix: "set " + varName + " first"},
			{Name: "GitHub token scopes", Pass: false, Fix: "set " + varName + " first"},
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/user", nil)
	if err != nil {
		return errorPair("GitHub token valid", "GitHub token scopes", fmt.Sprintf("build request: %v", err))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return errorPair("GitHub token valid", "GitHub token scopes",
			"GitHub API unreachable — check network connectivity")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return errorPair("GitHub token valid", "GitHub token scopes",
			"token is invalid or expired — generate a new one at https://github.com/settings/tokens")
	}
	if resp.StatusCode != http.StatusOK {
		return errorPair("GitHub token valid", "GitHub token scopes",
			fmt.Sprintf("unexpected status %d from GitHub API", resp.StatusCode))
	}

	var user struct {
		Login string `json:"login"`
	}
	json.NewDecoder(resp.Body).Decode(&user) //nolint:errcheck

	tokenResult := Result{Name: "GitHub token valid", Pass: true, Detail: "user: " + user.Login}

	scopeHeader := resp.Header.Get("X-OAuth-Scopes")
	if scopeHeader == "" {
		return []Result{
			tokenResult,
			{Name: "GitHub token scopes", Pass: true, Detail: "fine-grained token — scopes not inspectable via header"},
		}
	}
	if !strings.Contains(scopeHeader, "repo") {
		return []Result{
			tokenResult,
			{
				Name: "GitHub token scopes",
				Pass: false,
				Fix:  "token is missing 'repo' scope — re-generate at https://github.com/settings/tokens with repo + issues + pull_requests",
			},
		}
	}
	return []Result{
		tokenResult,
		{Name: "GitHub token scopes", Pass: true, Detail: "repo scope present"},
	}
}

// CheckConfig verifies the config file parses and passes deep validation.
func CheckConfig(cfgPath, skillsDir string) []Result {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return []Result{{
			Name: "config file valid",
			Pass: false,
			Fix:  fmt.Sprintf("%v — run `cypher validate --config %s` for details", err, cfgPath),
		}}
	}

	readableResult := Result{Name: "config file readable", Pass: true, Detail: cfgPath}

	issues := validate.Check(cfg, skillsDir)
	errCount := 0
	for _, i := range issues {
		if i.Severity == validate.Error {
			errCount++
		}
	}
	if errCount > 0 {
		return []Result{
			readableResult,
			{
				Name: "config file valid",
				Pass: false,
				Fix:  fmt.Sprintf("%d validation error(s) — run `cypher validate --config %s` for details", errCount, cfgPath),
			},
		}
	}
	return []Result{
		readableResult,
		{Name: "config file valid", Pass: true},
	}
}

// CheckDocker verifies the Docker socket is reachable and the worker image exists locally.
func CheckDocker(ctx context.Context, client *http.Client, apiBase, image string) []Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/v1.41/info", nil)
	if err != nil {
		return errorPair("Docker socket reachable", "worker image available",
			fmt.Sprintf("build request: %v", err))
	}
	resp, err := client.Do(req)
	if err != nil {
		return errorPair("Docker socket reachable", "worker image available",
			"Docker socket unreachable — is Docker running? check CYPHER_DOCKER_SOCKET")
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errorPair("Docker socket reachable", "worker image available",
			fmt.Sprintf("Docker returned status %d", resp.StatusCode))
	}
	socketResult := Result{Name: "Docker socket reachable", Pass: true}
	return []Result{socketResult, checkDockerImage(ctx, client, apiBase, image)}
}

func checkDockerImage(ctx context.Context, client *http.Client, apiBase, image string) Result {
	path := apiBase + "/v1.41/images/" + url.PathEscape(image) + "/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Result{Name: "worker image available", Pass: false, Fix: fmt.Sprintf("run: docker pull %s", image)}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Name: "worker image available", Pass: false, Fix: fmt.Sprintf("run: docker pull %s", image)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Result{
			Name: "worker image available",
			Pass: false,
			Fix:  fmt.Sprintf("image not found locally — run: docker pull %s", image),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{Name: "worker image available", Pass: false, Fix: fmt.Sprintf("unexpected Docker status %d", resp.StatusCode)}
	}
	return Result{Name: "worker image available", Pass: true, Detail: image}
}

// CheckOpenHands verifies the OpenHands endpoint is responding.
func CheckOpenHands(ctx context.Context, client *http.Client, baseURL string) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return Result{
			Name: "OpenHands endpoint responding",
			Pass: false,
			Fix:  fmt.Sprintf("invalid URL %q — check CYPHER_OPENHANDS_URL", baseURL),
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{
			Name: "OpenHands endpoint responding",
			Pass: false,
			Fix:  fmt.Sprintf("cannot reach %s — is OpenHands running? check CYPHER_OPENHANDS_URL", baseURL),
		}
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return Result{
			Name: "OpenHands endpoint responding",
			Pass: false,
			Fix:  fmt.Sprintf("OpenHands returned status %d — check container logs", resp.StatusCode),
		}
	}
	return Result{Name: "OpenHands endpoint responding", Pass: true, Detail: baseURL}
}

// CheckSecrets scans envVars for op:// references and verifies the 1Password CLI
// is installed and can resolve each secret. Returns no results when no op:// values
// are found (secrets are all plaintext — nothing to check).
func CheckSecrets(ctx context.Context, envVars []string, op *secrets.OnePassword) []Result {
	var opRefs []struct{ key, uri string }
	for _, key := range envVars {
		if v := strings.TrimSpace(os.Getenv(key)); strings.HasPrefix(v, "op://") {
			opRefs = append(opRefs, struct{ key, uri string }{key, v})
		}
	}
	if len(opRefs) == 0 {
		return nil
	}

	if !op.Available() {
		return []Result{{
			Name: "1Password CLI (op) installed",
			Pass: false,
			Fix:  "install the 1Password CLI: https://developer.1password.com/docs/cli/get-started/",
		}}
	}

	var results []Result
	results = append(results, Result{Name: "1Password CLI (op) installed", Pass: true})

	for _, ref := range opRefs {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := op.Resolve(tctx, ref.uri)
		cancel()
		name := ref.key + " accessible via op"
		if err != nil {
			results = append(results, Result{
				Name: name,
				Pass: false,
				Fix:  fmt.Sprintf("run `op signin` and verify the secret exists — %s", ref.uri),
			})
		} else {
			results = append(results, Result{Name: name, Pass: true})
		}
	}
	return results
}

// CheckPEMFile warns when CYPHER_GH_APP_PRIVATE_KEY_FILE is set, indicating
// the GitHub App private key is stored as plaintext on disk. Returns no results
// when the variable is unset (key not yet configured or stored via op://).
func CheckPEMFile() []Result {
	v := strings.TrimSpace(os.Getenv("CYPHER_GH_APP_PRIVATE_KEY_FILE"))
	if v == "" {
		return nil
	}
	return []Result{{
		Name: "GitHub App key storage",
		Pass: false,
		Fix:  "CYPHER_GH_APP_PRIVATE_KEY_FILE stores the key as a plaintext file — migrate to 1Password by re-running `cypher setup` and choosing 1Password when prompted",
	}}
}

// errorPair returns two failed Results, the second referencing the first as a dependency.
func errorPair(first, second, fix string) []Result {
	return []Result{
		{Name: first, Pass: false, Fix: fix},
		{Name: second, Pass: false, Fix: fmt.Sprintf("resolve %q first", first)},
	}
}
