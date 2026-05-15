package setup

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/armarquez/project-cypher/internal/config"
)

// Config is all the inputs Run needs.
type Config struct {
	// TargetRepo is the GitHub URL of the repository, e.g. https://github.com/owner/repo.
	TargetRepo string
	// EnvPath is the .env file to write credentials into (created if absent).
	EnvPath string
	// CypherDir is the directory for Cypher runtime artifacts (e.g. .cypher/).
	CypherDir string
	// APIBase overrides the GitHub API base URL (for tests).
	APIBase string
	// Client is the HTTP client to use (defaults to http.DefaultClient).
	Client *http.Client
	// Stdout receives progress output (defaults to os.Stdout).
	Stdout io.Writer
	// Stdin is used for interactive prompts (defaults to os.Stdin).
	Stdin io.Reader
	// OnServerReady is called once the local callback server is listening.
	// The port is passed so callers (e.g. tests) can trigger the callback directly.
	OnServerReady func(port int)
	// PEMStorage controls where the GitHub App private key is stored.
	// "file"        — write to <CypherDir>/app-key.pem (gitignored, plaintext on disk)
	// "1password"   — store in 1Password via `op item create`; write op:// URI to .env
	// ""            — prompt interactively (unless OnServerReady is set, then defaults to "file")
	PEMStorage string
	// OPVault is the 1Password vault name used when PEMStorage="1password".
	// Defaults to "Private".
	OPVault string
	// OpPath overrides the path to the `op` CLI binary. Defaults to "op" from PATH.
	OpPath string
}

func (c *Config) apiBase() string {
	if c.APIBase != "" {
		return c.APIBase
	}
	return "https://api.github.com"
}

func (c *Config) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Config) stdout() io.Writer {
	if c.Stdout != nil {
		return c.Stdout
	}
	return os.Stdout
}

func (c *Config) stdin() io.Reader {
	if c.Stdin != nil {
		return c.Stdin
	}
	return os.Stdin
}

func (c *Config) opVault() string {
	if c.OPVault != "" {
		return c.OPVault
	}
	return "Private"
}

func (c *Config) opBin() string {
	if c.OpPath != "" {
		return c.OpPath
	}
	return "op"
}

// AppCredentials holds everything returned by the GitHub App manifest exchange.
type AppCredentials struct {
	AppID         int64
	Slug          string
	PEM           string
	ClientID      string
	ClientSecret  string
	WebhookSecret string
}

// Manifest is the GitHub App manifest payload.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

// GenerateManifest builds the App manifest for the given owner/repo and callback URL.
func GenerateManifest(owner, repo, callbackURL string) Manifest {
	return Manifest{
		Name:        fmt.Sprintf("cypher-%s-%s", owner, repo),
		URL:         fmt.Sprintf("https://github.com/%s/%s", owner, repo),
		Description: fmt.Sprintf("Cypher orchestration agent for %s/%s", owner, repo),
		RedirectURL: callbackURL,
		Public:      false,
		DefaultPermissions: map[string]string{
			"contents":      "write",
			"issues":        "write",
			"pull_requests": "write",
			"metadata":      "read",
			"statuses":      "read",
		},
		// DefaultEvents intentionally omitted: GitHub requires a webhook URL when events
		// are declared in the manifest. Subscribe to events via the App settings page
		// once the webhook handler (issue #65) is deployed and a public URL is available.
	}
}

// ExchangeCode exchanges the OAuth code from the manifest callback for App credentials.
func ExchangeCode(ctx context.Context, client *http.Client, apiBase, code string) (*AppCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		apiBase+"/app-manifests/"+code+"/conversions", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		PEM           string `json:"pem"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &AppCredentials{
		AppID:         result.ID,
		Slug:          result.Slug,
		PEM:           result.PEM,
		ClientID:      result.ClientID,
		ClientSecret:  result.ClientSecret,
		WebhookSecret: result.WebhookSecret,
	}, nil
}

// ParsePrivateKey parses an RSA private key from PEM-encoded data.
func ParsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key: %w", err)
	}
	return key, nil
}

// MakeJWT creates a GitHub App JWT (RS256, valid 10 minutes) for the given app ID.
func MakeJWT(appID int64, key *rsa.PrivateKey) (string, error) {
	now := time.Now()
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", appID),
	})

	b64 := base64.RawURLEncoding
	header := b64.EncodeToString(headerJSON)
	payload := b64.EncodeToString(payloadJSON)
	sigInput := header + "." + payload

	hash := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return sigInput + "." + b64.EncodeToString(sig), nil
}

// PollInstallation polls GET /app/installations until an installation for owner appears.
// It returns the installation ID when found, or an error after the deadline.
func PollInstallation(ctx context.Context, client *http.Client, apiBase, jwtToken, owner string) (int64, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		id, err := tryGetInstallation(ctx, client, apiBase, jwtToken, owner)
		if err != nil {
			return 0, err
		}
		if id > 0 {
			return id, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return 0, fmt.Errorf("timed out waiting for installation — try running cypher setup again")
}

func tryGetInstallation(ctx context.Context, client *http.Client, apiBase, jwtToken, owner string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/app/installations", nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list installations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var installations []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&installations); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	for _, inst := range installations {
		if strings.EqualFold(inst.Account.Login, owner) {
			return inst.ID, nil
		}
	}
	return 0, nil
}

// StorePEMIn1Password stores pemContent as a concealed field in a 1Password item
// and returns the op:// URI to read it back. Uses `op item create --template -`
// so the multiline PEM is passed via stdin JSON — no shell-escaping issues.
// --upsert ensures re-running setup updates the item rather than creating a duplicate.
func StorePEMIn1Password(ctx context.Context, opPath, pemContent, slug, vault string) (string, error) {
	title := "cypher-" + slug + "-key"

	type opField struct {
		Label string `json:"label"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	type opTemplate struct {
		Title    string            `json:"title"`
		Vault    map[string]string `json:"vault"`
		Category string            `json:"category"`
		Fields   []opField         `json:"fields"`
	}

	tmpl := opTemplate{
		Title:    title,
		Vault:    map[string]string{"name": vault},
		Category: "API_CREDENTIAL",
		Fields: []opField{
			{Label: "private key", Type: "CONCEALED", Value: pemContent},
		},
	}
	templateJSON, err := json.Marshal(tmpl)
	if err != nil {
		return "", fmt.Errorf("marshal template: %w", err)
	}

	cmd := exec.CommandContext(ctx, opPath, "item", "create",
		"--template", "-",
		"--vault", vault,
		"--upsert",
	)
	cmd.Stdin = bytes.NewReader(templateJSON)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("op item create: %s", strings.TrimSpace(string(out)))
	}

	return fmt.Sprintf("op://%s/%s/private key", vault, title), nil
}

// WriteCredentials persists App credentials to .env and optionally to disk.
//
// pemRef controls where the private key is stored:
//   - if it starts with "op://" the PEM is not written to disk; CYPHER_GH_APP_PRIVATE_KEY is set to pemRef
//   - otherwise pemRef is treated as a file path; the PEM is written there; CYPHER_GH_APP_PRIVATE_KEY_FILE is set
func WriteCredentials(envPath, cypherDir string, creds *AppCredentials, installID int64, pemRef string) error {
	var updates map[string]string

	if strings.HasPrefix(pemRef, "op://") {
		updates = map[string]string{
			"CYPHER_GH_APP_ID":          fmt.Sprintf("%d", creds.AppID),
			"CYPHER_GH_INSTALLATION_ID": fmt.Sprintf("%d", installID),
			"CYPHER_GH_APP_PRIVATE_KEY": pemRef,
		}
	} else {
		if err := os.MkdirAll(cypherDir, 0o700); err != nil {
			return fmt.Errorf("create cypher dir: %w", err)
		}
		if err := os.WriteFile(pemRef, []byte(creds.PEM), 0o600); err != nil {
			return fmt.Errorf("write PEM: %w", err)
		}
		updates = map[string]string{
			"CYPHER_GH_APP_ID":               fmt.Sprintf("%d", creds.AppID),
			"CYPHER_GH_INSTALLATION_ID":      fmt.Sprintf("%d", installID),
			"CYPHER_GH_APP_PRIVATE_KEY_FILE": pemRef,
		}
	}

	if err := updateEnvFile(envPath, updates); err != nil {
		return fmt.Errorf("update .env: %w", err)
	}
	return nil
}

// updateEnvFile reads envPath (creating it if absent), updates or appends the
// given key=value pairs, and writes the result back.
func updateEnvFile(path string, updates map[string]string) error {
	var lines []string

	if data, err := os.ReadFile(path); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			key, _, found := strings.Cut(line, "=")
			if found {
				key = strings.TrimSpace(key)
				if _, ok := updates[key]; ok {
					// Replace this line; emit the updated value below.
					continue
				}
			}
			lines = append(lines, line)
		}
	}

	for k, v := range updates {
		lines = append(lines, k+"="+v)
	}

	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

// promptPEMStorage prints storage options to w and reads the user's choice from r.
// Returns "1password" or "file". Defaults to "1password" on empty input.
func promptPEMStorage(r io.Reader, w io.Writer) string {
	fmt.Fprintln(w, "  How should the GitHub App private key be stored?")
	fmt.Fprintln(w, "  [1] 1Password (recommended) — zero plaintext on disk")
	fmt.Fprintln(w, "  [2] File — .cypher/app-key.pem (gitignored, but plaintext on disk)")
	fmt.Fprint(w, "  Enter choice [1]: ")

	scanner := bufio.NewScanner(r)
	if scanner.Scan() && strings.TrimSpace(scanner.Text()) == "2" {
		return "file"
	}
	return "1password"
}

// Run executes the full setup flow: manifest → callback → exchange → install → store credentials.
func Run(ctx context.Context, cfg Config) error {
	owner, repo, err := config.ParseRepo(cfg.TargetRepo)
	if err != nil {
		return err
	}

	out := cfg.stdout()

	// Step 1: Start callback server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start callback server: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://localhost:%d/callback", port)

	state, err := randomHex(16)
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}

	manifest := GenerateManifest(owner, repo, callbackURL)

	codeCh := make(chan string, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		manifestJSON, _ := json.Marshal(manifest)
		io.WriteString(w, autoSubmitPage(string(manifestJSON), state)) //nolint:errcheck
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		once.Do(func() { codeCh <- code })
		fmt.Fprint(w, successPage())
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Shutdown(ctx) //nolint:errcheck

	if cfg.OnServerReady != nil {
		go cfg.OnServerReady(port)
	}

	// Step 2: Open browser.
	localURL := fmt.Sprintf("http://localhost:%d/", port)
	fmt.Fprintf(out, "\nStep 1: Create GitHub App\n")
	fmt.Fprintf(out, "  App name: cypher-%s-%s\n", owner, repo)
	fmt.Fprintf(out, "  Opening %s in your browser.\n", localURL)
	fmt.Fprintf(out, "  Review the permissions and click \"Create GitHub App\" to continue.\n\n")
	fmt.Fprintf(out, "  Waiting for callback... (ctrl-c to cancel)\n\n")

	if cfg.OnServerReady == nil {
		if err := openBrowser(localURL); err != nil {
			fmt.Fprintf(out, "  Could not open browser automatically.\n")
			fmt.Fprintf(out, "  Please open this URL manually: %s\n\n", localURL)
		}
	}

	// Step 3: Wait for code.
	var code string
	select {
	case code = <-codeCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Minute):
		return fmt.Errorf("timed out waiting for GitHub callback")
	}
	if code == "" {
		return fmt.Errorf("GitHub returned empty code — setup cancelled or failed")
	}

	// Step 4: Exchange code.
	creds, err := ExchangeCode(ctx, cfg.client(), cfg.apiBase(), code)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}
	fmt.Fprintf(out, "  ✓ App created (app_id: %d)\n\n", creds.AppID)

	// Step 5: Open installation URL.
	installURL := fmt.Sprintf("https://github.com/apps/%s/installations/new", creds.Slug)
	fmt.Fprintf(out, "Step 2: Install app on %s/%s\n", owner, repo)
	fmt.Fprintf(out, "  Opening %s\n", installURL)
	fmt.Fprintf(out, "  Click \"Install\" to grant access to the repository.\n\n")
	fmt.Fprintf(out, "  Waiting...\n\n")

	if cfg.OnServerReady == nil {
		if err := openBrowser(installURL); err != nil {
			fmt.Fprintf(out, "  Please open this URL manually: %s\n\n", installURL)
		}
	}

	// Step 6: Poll for installation.
	privateKey, err := ParsePrivateKey(creds.PEM)
	if err != nil {
		return fmt.Errorf("parse app private key: %w", err)
	}
	jwtToken, err := MakeJWT(creds.AppID, privateKey)
	if err != nil {
		return fmt.Errorf("generate JWT: %w", err)
	}

	installID, err := PollInstallation(ctx, cfg.client(), cfg.apiBase(), jwtToken, owner)
	if err != nil {
		return fmt.Errorf("poll installation: %w", err)
	}
	fmt.Fprintf(out, "  ✓ Installed (installation_id: %d)\n\n", installID)

	envPath := cfg.EnvPath
	if envPath == "" {
		envPath = ".env"
	}
	cypherDir := cfg.CypherDir
	if cypherDir == "" {
		cypherDir = ".cypher"
	}

	// Step 7: Determine PEM storage and store the key.
	// Resolve the storage mode: explicit flag > test mode default > interactive prompt.
	pemStorage := cfg.PEMStorage
	if pemStorage == "" && cfg.OnServerReady != nil {
		pemStorage = "file" // test mode: avoid blocking on stdin
	}

	var pemRef string
	storeInOP := func() error {
		vault := cfg.opVault()
		fmt.Fprintf(out, "  Storing in 1Password vault %q...\n", vault)
		var opErr error
		pemRef, opErr = StorePEMIn1Password(ctx, cfg.opBin(), creds.PEM, creds.Slug, vault)
		if opErr != nil {
			return fmt.Errorf("store PEM in 1Password: %w", opErr)
		}
		fmt.Fprintf(out, "  ✓ Stored: %s\n\n", pemRef)
		return nil
	}

	switch pemStorage {
	case "1password":
		fmt.Fprintf(out, "Step 3: Store private key\n")
		if err = storeInOP(); err != nil {
			return err
		}
	case "file":
		pemRef = filepath.Join(cypherDir, "app-key.pem")
	default:
		// Interactive prompt.
		fmt.Fprintf(out, "Step 3: Store private key\n")
		if promptPEMStorage(cfg.stdin(), out) == "1password" {
			if err = storeInOP(); err != nil {
				return err
			}
		} else {
			pemRef = filepath.Join(cypherDir, "app-key.pem")
		}
	}

	if err = WriteCredentials(envPath, cypherDir, creds, installID, pemRef); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	stepLabel := "Step 3"
	if pemStorage == "1password" || strings.HasPrefix(pemRef, "op://") {
		stepLabel = "Step 4"
	}
	fmt.Fprintf(out, "%s: Write credentials\n", stepLabel)
	fmt.Fprintf(out, "  ✓ %s updated:\n", envPath)
	fmt.Fprintf(out, "      CYPHER_GH_APP_ID=%d\n", creds.AppID)
	if strings.HasPrefix(pemRef, "op://") {
		fmt.Fprintf(out, "      CYPHER_GH_APP_PRIVATE_KEY=%s\n", pemRef)
	} else {
		fmt.Fprintf(out, "      CYPHER_GH_APP_PRIVATE_KEY_FILE=%s\n", pemRef)
	}
	fmt.Fprintf(out, "      CYPHER_GH_INSTALLATION_ID=%d\n\n", installID)
	fmt.Fprintf(out, "Run `cypher doctor` to verify your environment.\n")

	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		// Try WSL2 bridge first, then xdg-open.
		if _, err := exec.LookPath("wslview"); err == nil {
			cmd = exec.Command("wslview", url)
		} else if _, err := exec.LookPath("cmd.exe"); err == nil {
			cmd = exec.Command("cmd.exe", "/c", "start", url)
		} else {
			cmd = exec.Command("xdg-open", url)
		}
	}
	return cmd.Start()
}

func autoSubmitPage(manifestJSON, state string) string {
	return `<!DOCTYPE html>
<html>
<head><title>Cypher Setup</title></head>
<body>
<p>Redirecting to GitHub to create your App...</p>
<form id="f" method="post" action="https://github.com/settings/apps/new?state=` + state + `">
  <input type="hidden" name="manifest" value='` + manifestJSON + `'>
</form>
<script>document.getElementById("f").submit();</script>
</body>
</html>`
}

func successPage() string {
	return `<!DOCTYPE html>
<html>
<head><title>Cypher Setup</title></head>
<body>
<h2>GitHub App created successfully.</h2>
<p>You can close this tab and return to your terminal.</p>
</body>
</html>`
}
