package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/harness-community/drone-kimia/internal/envutil"
)

const (
	defaultConfigDir     = "/home/kimia/.docker"
	dockerHubRegistry    = "https://index.docker.io/v1/"
	defaultHTTPUserAgent = "drone-kimia"
)

// Provider identifies the registry authentication flow used by Prepare.
type Provider string

const (
	ProviderDocker Provider = "docker"
	ProviderGAR    Provider = "gar"
	ProviderECR    Provider = "ecr"
	ProviderACR    Provider = "acr"
)

// ECRClient is the subset of the AWS ECR client used to obtain Docker
// credentials. It is exposed so the caller's tests can supply a deterministic
// client without contacting AWS.
type ECRClient interface {
	GetAuthorizationToken(context.Context, *ecr.GetAuthorizationTokenInput, ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

// Options controls authentication preparation. Registry is the destination
// registry from the plugin configuration. SourceConfigDir is read-only input;
// ConfigDir is the private output directory written for Kimia. Push must be
// false for build-only and tar-export operations so destination credentials are
// not unnecessarily requested.
type Options struct {
	Provider        Provider
	Registry        string
	Push            bool
	SourceConfigDir string
	ConfigDir       string
	HTTPClient      *http.Client
	ECRClient       ECRClient
}

// Result describes the Docker configuration prepared for Kimia. Registry can
// be resolved by a provider (for example GAR from PLUGIN_LOCATION or ECR from
// GetAuthorizationToken) and should be used by the caller when non-empty.
type Result struct {
	Registry          string
	ConfigDir         string
	ConfigPath        string
	PushAuthenticated bool
}

type credential struct {
	Registry string
	Username string
	Password string
}

// Prepare merges existing Docker configuration, optional base-image
// credentials, and provider-specific push credentials into the Docker config
// consumed by Kimia/BuildKit. Explicit push credentials are written last and
// therefore win when a base-image credential targets the same registry.
func Prepare(ctx context.Context, opts Options) (Result, error) {
	provider := Provider(strings.ToLower(strings.TrimSpace(string(opts.Provider))))
	if provider == "" {
		provider = ProviderDocker
	}
	if !validProvider(provider) {
		return Result{}, fmt.Errorf("unsupported authentication provider %q", opts.Provider)
	}

	sourceConfigDir := strings.TrimSpace(opts.SourceConfigDir)
	if sourceConfigDir == "" {
		sourceConfigDir = strings.TrimSpace(os.Getenv("DOCKER_CONFIG"))
	}
	sourceConfigPath := ""
	if sourceConfigDir != "" {
		sourceConfigPath = filepath.Join(sourceConfigDir, "config.json")
	}

	configDir := strings.TrimSpace(opts.ConfigDir)
	if configDir == "" {
		configDir = defaultConfigDir
	}
	configPath := filepath.Join(configDir, "config.json")

	document, err := loadDockerConfig(sourceConfigPath)
	if err != nil {
		return Result{}, err
	}

	base, hasBase, err := baseImageCredential(provider)
	if err != nil {
		return Result{}, err
	}
	if hasBase {
		if err := document.setCredential(base); err != nil {
			return Result{}, err
		}
	}

	result := Result{
		Registry:   strings.TrimSpace(opts.Registry),
		ConfigDir:  configDir,
		ConfigPath: configPath,
	}
	if provider != ProviderDocker {
		result.Registry = canonicalRegistry(result.Registry)
	}
	if result.Registry == "" && provider == ProviderGAR {
		result.Registry = garRegistry("")
	}

	if opts.Push {
		pushCredential, resolvedRegistry, ok, resolveErr := resolvePushCredential(ctx, provider, result.Registry, opts)
		if resolveErr != nil {
			return Result{}, resolveErr
		}
		if resolvedRegistry != "" {
			result.Registry = resolvedRegistry
		}
		if ok {
			if err := document.setCredential(pushCredential); err != nil {
				return Result{}, err
			}
			result.PushAuthenticated = true
		} else if provider == ProviderGAR && result.Registry != "" && !document.hasCredentialMechanism(result.Registry) {
			// Preserve Kaniko GAR's ambient Workload Identity behavior. Kimia's
			// BuildKit image bundles docker-credential-gcr, but BuildKit only
			// invokes it when the Docker config selects the helper.
			if err := document.setCredentialHelper(result.Registry, "gcr"); err != nil {
				return Result{}, err
			}
			result.PushAuthenticated = true
		}
	}

	if err := document.writeAtomic(configDir, configPath); err != nil {
		return Result{}, err
	}
	if err := os.Setenv("DOCKER_CONFIG", configDir); err != nil {
		return Result{}, fmt.Errorf("set DOCKER_CONFIG: %w", err)
	}
	return result, nil
}

func validProvider(provider Provider) bool {
	switch provider {
	case ProviderDocker, ProviderGAR, ProviderECR, ProviderACR:
		return true
	default:
		return false
	}
}

func resolvePushCredential(ctx context.Context, provider Provider, registry string, opts Options) (credential, string, bool, error) {
	switch provider {
	case ProviderDocker:
		return resolveDockerCredential(registry)
	case ProviderGAR:
		return resolveGARCredential(ctx, registry, httpClient(opts.HTTPClient))
	case ProviderECR:
		return resolveECRCredential(ctx, registry, opts.ECRClient)
	case ProviderACR:
		return resolveACRCredential(ctx, registry, httpClient(opts.HTTPClient))
	default:
		return credential{}, "", false, fmt.Errorf("unsupported authentication provider %q", provider)
	}
}

func resolveDockerCredential(registry string) (credential, string, bool, error) {
	resolved := strings.TrimSpace(registry)
	authRegistry := resolved
	if authRegistry == "" {
		authRegistry = dockerHubRegistry
	}

	username := envutil.First("PLUGIN_USERNAME", "DOCKER_USERNAME")
	password := envutil.First("PLUGIN_PASSWORD", "DOCKER_PASSWORD")
	accessToken := envutil.First("ACCESS_TOKEN")
	if password != "" || username != "" {
		if username == "" || password == "" {
			return credential{}, "", false, fmt.Errorf("both registry username and password must be provided")
		}
		return credential{Registry: authRegistry, Username: username, Password: password}, resolved, true, nil
	}
	if accessToken != "" {
		return credential{Registry: authRegistry, Username: "oauth2accesstoken", Password: accessToken}, resolved, true, nil
	}
	return credential{}, resolved, false, nil
}

func baseImageCredential(provider Provider) (credential, bool, error) {
	registryKeys := []string{"PLUGIN_DOCKER_REGISTRY", "PLUGIN_BASE_IMAGE_REGISTRY", "DOCKER_BASE_IMAGE_REGISTRY"}
	usernameKeys := []string{"PLUGIN_DOCKER_USERNAME", "PLUGIN_BASE_IMAGE_USERNAME", "DOCKER_BASE_IMAGE_USERNAME"}
	passwordKeys := []string{"PLUGIN_DOCKER_PASSWORD", "PLUGIN_BASE_IMAGE_PASSWORD", "DOCKER_BASE_IMAGE_PASSWORD"}

	// The cloud-specific Kaniko images historically used the unqualified
	// Docker variables for the base-image connector. Keep them as low-priority
	// fallbacks without stealing generic Docker push credentials.
	if provider != ProviderDocker {
		registryKeys = append(registryKeys, "DOCKER_REGISTRY")
		usernameKeys = append(usernameKeys, "DOCKER_USERNAME")
		passwordKeys = append(passwordKeys, "DOCKER_PASSWORD")
	}
	if provider == ProviderECR {
		usernameKeys = append(usernameKeys, "PLUGIN_USERNAME")
		passwordKeys = append(passwordKeys, "PLUGIN_PASSWORD")
	}

	registry := envutil.First(registryKeys...)
	username := envutil.First(usernameKeys...)
	password := envutil.First(passwordKeys...)
	if registry == "" && username == "" && password == "" {
		return credential{}, false, nil
	}
	if registry == "" || username == "" || password == "" {
		return credential{}, false, fmt.Errorf("base-image registry, username, and password must all be provided")
	}
	return credential{Registry: registry, Username: username, Password: password}, true, nil
}

type dockerConfigDocument struct {
	raw   map[string]json.RawMessage
	auths map[string]json.RawMessage
}

func loadDockerConfig(configPath string) (*dockerConfigDocument, error) {
	data := []byte(strings.TrimSpace(envutil.First("PLUGIN_CONFIG", "DOCKER_PLUGIN_CONFIG")))
	if len(data) == 0 && configPath != "" {
		var err error
		data, err = os.ReadFile(configPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read Docker config %q: %w", configPath, err)
		}
		if os.IsNotExist(err) {
			data = nil
		}
	}

	document := &dockerConfigDocument{
		raw:   make(map[string]json.RawMessage),
		auths: make(map[string]json.RawMessage),
	}
	if len(data) == 0 {
		return document, nil
	}
	if err := json.Unmarshal(data, &document.raw); err != nil {
		return nil, fmt.Errorf("parse Docker config: %w", err)
	}
	if document.raw == nil {
		document.raw = make(map[string]json.RawMessage)
	}
	if rawAuths, ok := document.raw["auths"]; ok && len(rawAuths) > 0 && string(rawAuths) != "null" {
		if err := json.Unmarshal(rawAuths, &document.auths); err != nil {
			return nil, fmt.Errorf("parse Docker config auths: %w", err)
		}
	}
	return document, nil
}

func (document *dockerConfigDocument) setCredential(value credential) error {
	registry := canonicalRegistry(value.Registry)
	if registry == "" {
		return fmt.Errorf("registry must be provided for authentication")
	}
	if value.Username == "" || value.Password == "" {
		return fmt.Errorf("username and password must be provided for registry %s", registry)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(value.Username + ":" + value.Password))
	entry, err := json.Marshal(map[string]string{"auth": encoded})
	if err != nil {
		return fmt.Errorf("encode Docker credential for %s: %w", registry, err)
	}
	if err := document.removeCredentialHelper(registry); err != nil {
		return err
	}
	// Docker and BuildKit prefer a global credential store over inline auths.
	// Remove it whenever connector credentials are overlaid so the explicit
	// credential written here is actually authoritative.
	delete(document.raw, "credsStore")
	document.auths[registry] = entry
	return nil
}

func (document *dockerConfigDocument) removeCredentialHelper(registry string) error {
	rawHelpers, ok := document.raw["credHelpers"]
	if !ok || len(rawHelpers) == 0 || string(rawHelpers) == "null" {
		return nil
	}
	helpers := make(map[string]string)
	if err := json.Unmarshal(rawHelpers, &helpers); err != nil {
		return fmt.Errorf("parse Docker config credential helpers: %w", err)
	}
	for key := range helpers {
		if canonicalRegistry(key) == registry {
			delete(helpers, key)
		}
	}
	encoded, err := json.Marshal(helpers)
	if err != nil {
		return fmt.Errorf("encode Docker config credential helpers: %w", err)
	}
	document.raw["credHelpers"] = encoded
	return nil
}

func (document *dockerConfigDocument) setCredentialHelper(registry, helper string) error {
	registry = canonicalRegistry(registry)
	if registry == "" || strings.TrimSpace(helper) == "" {
		return fmt.Errorf("registry and credential helper must be provided")
	}
	helpers := make(map[string]string)
	if rawHelpers, ok := document.raw["credHelpers"]; ok && len(rawHelpers) > 0 && string(rawHelpers) != "null" {
		if err := json.Unmarshal(rawHelpers, &helpers); err != nil {
			return fmt.Errorf("parse Docker config credential helpers: %w", err)
		}
	}
	helpers[registry] = helper
	encoded, err := json.Marshal(helpers)
	if err != nil {
		return fmt.Errorf("encode Docker config credential helpers: %w", err)
	}
	document.raw["credHelpers"] = encoded
	return nil
}

func (document *dockerConfigDocument) hasCredentialMechanism(registry string) bool {
	registry = canonicalRegistry(registry)
	for key := range document.auths {
		if canonicalRegistry(key) == registry {
			return true
		}
	}
	if rawHelpers, ok := document.raw["credHelpers"]; ok {
		helpers := make(map[string]string)
		if json.Unmarshal(rawHelpers, &helpers) == nil {
			for key, helper := range helpers {
				if canonicalRegistry(key) == registry && helper != "" {
					return true
				}
			}
		}
	}
	return false
}

func (document *dockerConfigDocument) writeAtomic(configDir, configPath string) error {
	encodedAuths, err := json.Marshal(document.auths)
	if err != nil {
		return fmt.Errorf("encode Docker config auths: %w", err)
	}
	document.raw["auths"] = encodedAuths
	data, err := json.Marshal(document.raw)
	if err != nil {
		return fmt.Errorf("encode Docker config: %w", err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create Docker config directory %q: %w", configDir, err)
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		return fmt.Errorf("secure Docker config directory %q: %w", configDir, err)
	}
	temporary, err := os.CreateTemp(configDir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Docker config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary Docker config: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary Docker config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary Docker config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary Docker config: %w", err)
	}
	if err := os.Rename(temporaryPath, configPath); err != nil {
		return fmt.Errorf("install Docker config %q: %w", configPath, err)
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		return fmt.Errorf("secure Docker config %q: %w", configPath, err)
	}
	return nil
}

func canonicalRegistry(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return canonicalRegistryHost(parsed.Host)
	}
	host, _, _ := strings.Cut(strings.TrimSuffix(value, "/"), "/")
	return canonicalRegistryHost(host)
}

func canonicalRegistryHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return dockerHubRegistry
	default:
		return host
	}
}

func httpClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func addUserAgent(request *http.Request) {
	request.Header.Set("User-Agent", defaultHTTPUserAgent)
}
