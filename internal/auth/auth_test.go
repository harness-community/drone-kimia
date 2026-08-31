package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

func TestPrepareDockerMergesConfigBaseAndPush(t *testing.T) {
	clearAuthEnvironment(t)
	configDir := t.TempDir()
	if err := os.Chmod(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGIN_CONFIG", `{"auths":{"existing.example":{"auth":"ZXhpc3Rpbmc6c2VjcmV0"}},"credHelpers":{"helper.example":"example"},"experimental":"enabled"}`)
	t.Setenv("PLUGIN_DOCKER_REGISTRY", "base.example")
	t.Setenv("PLUGIN_DOCKER_USERNAME", "base-user")
	t.Setenv("PLUGIN_DOCKER_PASSWORD", "base-password")
	t.Setenv("PLUGIN_REGISTRY", "push.example")
	t.Setenv("PLUGIN_USERNAME", "push-user")
	t.Setenv("PLUGIN_PASSWORD", "push-password")

	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderDocker,
		Registry:  "push.example",
		Push:      true,
		ConfigDir: configDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PushAuthenticated {
		t.Fatal("expected explicit push authentication")
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, "existing.example", "existing", "secret")
	assertTestAuth(t, document.Auths, "base.example", "base-user", "base-password")
	assertTestAuth(t, document.Auths, "push.example", "push-user", "push-password")
	if document.CredHelpers["helper.example"] != "example" {
		t.Fatalf("credHelpers were not preserved: %#v", document.CredHelpers)
	}
	if document.Experimental != "enabled" {
		t.Fatalf("unknown top-level field was not preserved: %q", document.Experimental)
	}
	info, err := os.Stat(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	directoryInfo, err := os.Stat(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("config directory mode = %o, want 700", directoryInfo.Mode().Perm())
	}
	if got := os.Getenv("DOCKER_CONFIG"); got != configDir {
		t.Fatalf("DOCKER_CONFIG = %q, want %q", got, configDir)
	}
}

func TestPrepareReadsSourceConfigWithoutMutatingIt(t *testing.T) {
	clearAuthEnvironment(t)
	sourceDir := t.TempDir()
	outputDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "config.json")
	sourceData := []byte(`{"auths":{"source.example":{"auth":"c291cmNlOnNlY3JldA=="}},"experimental":"source"}`)
	if err := os.WriteFile(sourcePath, sourceData, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sourcePath, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sourceDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGIN_USERNAME", "push-user")
	t.Setenv("PLUGIN_PASSWORD", "push-password")

	result, err := Prepare(context.Background(), Options{
		Provider:        ProviderDocker,
		Registry:        "push.example",
		Push:            true,
		SourceConfigDir: sourceDir,
		ConfigDir:       outputDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, "source.example", "source", "secret")
	assertTestAuth(t, document.Auths, "push.example", "push-user", "push-password")

	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(sourceData) {
		t.Fatalf("source config was modified: %s", after)
	}
	fileInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o640 {
		t.Fatalf("source config mode = %o, want 640", fileInfo.Mode().Perm())
	}
	directoryInfo, err := os.Stat(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o750 {
		t.Fatalf("source directory mode = %o, want 750", directoryInfo.Mode().Perm())
	}
}

func TestPrepareExplicitCredentialRemovesGlobalCredentialStore(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_CONFIG", `{"credsStore":"desktop","credHelpers":{"registry.example":"desktop","other.example":"other"}}`)
	t.Setenv("PLUGIN_USERNAME", "push-user")
	t.Setenv("PLUGIN_PASSWORD", "push-password")

	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderDocker,
		Registry:  "registry.example",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	if document.CredsStore != "" {
		t.Fatalf("credsStore = %q, want removed", document.CredsStore)
	}
	if _, ok := document.CredHelpers["registry.example"]; ok {
		t.Fatal("registry-specific helper was not removed")
	}
	if document.CredHelpers["other.example"] != "other" {
		t.Fatalf("unrelated credential helper was not preserved: %#v", document.CredHelpers)
	}
	assertTestAuth(t, document.Auths, "registry.example", "push-user", "push-password")
}

func TestPreparePushCredentialWinsForSameRegistry(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_DOCKER_REGISTRY", "registry.example")
	t.Setenv("PLUGIN_DOCKER_USERNAME", "base-user")
	t.Setenv("PLUGIN_DOCKER_PASSWORD", "base-password")
	t.Setenv("PLUGIN_USERNAME", "push-user")
	t.Setenv("PLUGIN_PASSWORD", "push-password")

	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderDocker,
		Registry:  "registry.example",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, "registry.example", "push-user", "push-password")
}

func TestPrepareBuildOnlyKeepsBaseAndSkipsPush(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_DOCKER_REGISTRY", "base.example")
	t.Setenv("PLUGIN_DOCKER_USERNAME", "base-user")
	t.Setenv("PLUGIN_DOCKER_PASSWORD", "base-password")
	t.Setenv("PLUGIN_USERNAME", "push-user")
	t.Setenv("PLUGIN_PASSWORD", "push-password")

	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderDocker,
		Registry:  "push.example",
		Push:      false,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PushAuthenticated {
		t.Fatal("build-only mode must not resolve push authentication")
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, "base.example", "base-user", "base-password")
	if _, ok := document.Auths["push.example"]; ok {
		t.Fatal("push credential was written during build-only mode")
	}
}

func TestPrepareBuildOnlyResolvesRegistryMetadataWithoutCloudCalls(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_LOCATION", "us-central1")
	garResult, err := Prepare(context.Background(), Options{Provider: ProviderGAR, Push: false, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if garResult.Registry != "us-central1-docker.pkg.dev" {
		t.Fatalf("GAR registry = %q", garResult.Registry)
	}

	clearAuthEnvironment(t)
	ecrResult, err := Prepare(context.Background(), Options{Provider: ProviderECR, Registry: "123456789012.dkr.ecr.us-east-1.amazonaws.com", Push: false, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if ecrResult.Registry != "123456789012.dkr.ecr.us-east-1.amazonaws.com" {
		t.Fatalf("ECR registry = %q", ecrResult.Registry)
	}

	clearAuthEnvironment(t)
	acrResult, err := Prepare(context.Background(), Options{Provider: ProviderACR, Registry: "registry.azurecr.io", Push: false, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if acrResult.Registry != "registry.azurecr.io" {
		t.Fatalf("ACR registry = %q", acrResult.Registry)
	}
}

func TestPrepareRejectsPartialBaseCredential(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_DOCKER_REGISTRY", "base.example")
	t.Setenv("PLUGIN_DOCKER_USERNAME", "base-user")
	_, err := Prepare(context.Background(), Options{Provider: ProviderDocker, ConfigDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "must all be provided") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareGARJSONKey(t *testing.T) {
	clearAuthEnvironment(t)
	key := `{"type":"service_account","project_id":"example"}`
	t.Setenv("PLUGIN_JSON_KEY", base64.StdEncoding.EncodeToString([]byte(key)))
	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderGAR,
		Registry:  "us-docker.pkg.dev",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, "us-docker.pkg.dev", "_json_key", key)
}

func TestPrepareGARRejectsPartialOIDCInputs(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_OIDC_TOKEN_ID", "token")
	t.Setenv("PLUGIN_PROJECT_NUMBER", "project")
	_, err := Prepare(context.Background(), Options{
		Provider:  ProviderGAR,
		Registry:  "us-docker.pkg.dev",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "GAR OIDC requires") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareGARRejectsNonGoogleRegistryBeforeStoringKey(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_JSON_KEY", `{"type":"service_account"}`)
	_, err := Prepare(context.Background(), Options{
		Provider:  ProviderGAR,
		Registry:  "attacker.example",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "-docker.pkg.dev") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareGARAmbientCredentialsConfigureBundledHelper(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_CONFIG", `{"credsStore":"desktop"}`)
	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderGAR,
		Registry:  "us-docker.pkg.dev",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	if got := document.CredHelpers["us-docker.pkg.dev"]; got != "gcr" {
		t.Fatalf("GAR credential helper = %q, want gcr", got)
	}
}

func TestPrepareGARAmbientFlowPreservesSuppliedRegistryAuth(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_CONFIG", `{"auths":{"us-docker.pkg.dev":{"auth":"dXNlcjpwYXNz"}}}`)
	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderGAR,
		Registry:  "us-docker.pkg.dev",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	if _, ok := document.CredHelpers["us-docker.pkg.dev"]; ok {
		t.Fatal("ambient helper replaced a supplied registry authentication entry")
	}
	assertTestAuth(t, document.Auths, "us-docker.pkg.dev", "user", "pass")
}

func TestPrepareECRUsesAuthorizationToken(t *testing.T) {
	clearAuthEnvironment(t)
	client := fakeECRClient{
		output: &ecr.GetAuthorizationTokenOutput{AuthorizationData: []ecrtypes.AuthorizationData{{
			AuthorizationToken: aws.String(base64.StdEncoding.EncodeToString([]byte("AWS:ecr-password"))),
			ProxyEndpoint:      aws.String("https://123456789012.dkr.ecr.us-east-1.amazonaws.com"),
		}}},
	}
	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderECR,
		Push:      true,
		ConfigDir: t.TempDir(),
		ECRClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Registry != "123456789012.dkr.ecr.us-east-1.amazonaws.com" {
		t.Fatalf("resolved registry = %q", result.Registry)
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, result.Registry, "AWS", "ecr-password")
}

func TestECRSessionTokenUsesHarnessPluginAliasFirst(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("AWS_SESSION_TOKEN", "aws-fallback")
	t.Setenv("PLUGIN_SESSION_TOKEN", "harness-token")
	if got := ecrSessionToken(); got != "harness-token" {
		t.Fatalf("ecrSessionToken() = %q, want Harness plugin token", got)
	}

	t.Setenv("PLUGIN_SESSION_TOKEN", "")
	if got := ecrSessionToken(); got != "aws-fallback" {
		t.Fatalf("ecrSessionToken() fallback = %q, want AWS session token", got)
	}
}

func TestPrivateECRRegistryRegion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		registry string
		region   string
	}{
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com", "us-east-1"},
		{"https://123456789012.dkr.ecr-fips.us-gov-west-1.amazonaws.com/v2/", "us-gov-west-1"},
		{"123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn", "cn-north-1"},
	} {
		region, err := privateECRRegistryRegion(test.registry)
		if err != nil {
			t.Fatalf("privateECRRegistryRegion(%q): %v", test.registry, err)
		}
		if region != test.region {
			t.Fatalf("privateECRRegistryRegion(%q) = %q, want %q", test.registry, region, test.region)
		}
	}
	if _, err := privateECRRegistryRegion("public.ecr.aws"); err == nil {
		t.Fatal("public ECR registry unexpectedly accepted as private ECR")
	}
}

func TestPrepareECRRejectsConfiguredRegionMismatch(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("PLUGIN_REGION", "us-west-2")
	_, err := Prepare(context.Background(), Options{
		Provider:  ProviderECR,
		Registry:  "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		Push:      true,
		ConfigDir: t.TempDir(),
		ECRClient: fakeECRClient{},
	})
	if err == nil || !strings.Contains(err.Error(), "configured region") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareECRRejectsNonAWSRegistry(t *testing.T) {
	clearAuthEnvironment(t)
	_, err := Prepare(context.Background(), Options{
		Provider:  ProviderECR,
		Registry:  "attacker.example",
		Push:      true,
		ConfigDir: t.TempDir(),
		ECRClient: fakeECRClient{},
	})
	if err == nil || !strings.Contains(err.Error(), "private ECR registry") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareACRDirectServicePrincipal(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("SERVICE_PRINCIPAL_CLIENT_ID", "client")
	t.Setenv("SERVICE_PRINCIPAL_CLIENT_SECRET", "secret")
	result, err := Prepare(context.Background(), Options{
		Provider:  ProviderACR,
		Registry:  "registry.azurecr.io",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	document := readTestConfig(t, result.ConfigPath)
	assertTestAuth(t, document.Auths, "registry.azurecr.io", "client", "secret")
}

func TestPrepareACRRejectsNonAzureRegistry(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("SERVICE_PRINCIPAL_CLIENT_ID", "client")
	t.Setenv("SERVICE_PRINCIPAL_CLIENT_SECRET", "secret")
	_, err := Prepare(context.Background(), Options{
		Provider:  ProviderACR,
		Registry:  "attacker.example",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "Azure Container Registry") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareACRRejectsSovereignRegistryInPublicOnlyMode(t *testing.T) {
	clearAuthEnvironment(t)
	t.Setenv("SERVICE_PRINCIPAL_CLIENT_ID", "client")
	t.Setenv("SERVICE_PRINCIPAL_CLIENT_SECRET", "secret")
	_, err := Prepare(context.Background(), Options{
		Provider:  ProviderACR,
		Registry:  "registry.azurecr.us",
		Push:      true,
		ConfigDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "Azure Container Registry") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestCanonicalRegistryStripsURLPaths(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
	}{
		{"https://Registry.Example/v2/", "registry.example"},
		{"http://Registry.Example:5000/custom/path", "registry.example:5000"},
		{"registry.example/v2", "registry.example"},
		{"https://index.docker.io/v1/", dockerHubRegistry},
	} {
		if got := canonicalRegistry(test.input); got != test.want {
			t.Fatalf("canonicalRegistry(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

type fakeECRClient struct {
	output *ecr.GetAuthorizationTokenOutput
	err    error
}

func (client fakeECRClient) GetAuthorizationToken(context.Context, *ecr.GetAuthorizationTokenInput, ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
	return client.output, client.err
}

type testDockerConfig struct {
	Auths        map[string]testDockerAuth `json:"auths"`
	CredHelpers  map[string]string         `json:"credHelpers"`
	CredsStore   string                    `json:"credsStore"`
	Experimental string                    `json:"experimental"`
}

type testDockerAuth struct {
	Auth string `json:"auth"`
}

func readTestConfig(t *testing.T, path string) testDockerConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var document testDockerConfig
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func assertTestAuth(t *testing.T, auths map[string]testDockerAuth, registry, username, password string) {
	t.Helper()
	entry, ok := auths[canonicalRegistry(registry)]
	if !ok {
		t.Fatalf("auth for %q not found in %#v", registry, auths)
	}
	decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(decoded), username+":"+password; got != want {
		t.Fatalf("auth for %q = %q, want %q", registry, got, want)
	}
}

func clearAuthEnvironment(t *testing.T) {
	t.Helper()
	keys := []string{
		"ACCESS_TOKEN", "AWS_ACCESS_KEY_ID", "AWS_REGION", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AZURE_APP_ID", "AZURE_AUTHORITY_HOST", "AZURE_CLIENT_ID", "AZURE_TENANT_ID",
		"CLIENT_CERTIFICATE", "CLIENT_ID", "CLIENT_SECRET", "DOCKER_BASE_IMAGE_PASSWORD",
		"DOCKER_BASE_IMAGE_REGISTRY", "DOCKER_BASE_IMAGE_USERNAME", "DOCKER_CONFIG", "DOCKER_PASSWORD",
		"DOCKER_PLUGIN_CONFIG", "DOCKER_REGISTRY", "DOCKER_USERNAME", "ECR_ACCESS_KEY", "ECR_REGION",
		"ECR_SECRET_KEY", "GCR_JSON_KEY", "GOOGLE_CREDENTIALS", "PLUGIN_ACCESS_KEY", "PLUGIN_ASSUME_ROLE",
		"PLUGIN_AZURE_AUTHORITY_HOST", "PLUGIN_BASE_IMAGE_PASSWORD", "PLUGIN_BASE_IMAGE_REGISTRY",
		"PLUGIN_BASE_IMAGE_USERNAME", "PLUGIN_CLIENT_CERTIFICATE", "PLUGIN_CLIENT_ID", "PLUGIN_CLIENT_SECRET",
		"PLUGIN_CONFIG", "PLUGIN_DOCKER_PASSWORD", "PLUGIN_DOCKER_REGISTRY", "PLUGIN_DOCKER_USERNAME",
		"PLUGIN_EXTERNAL_ID", "PLUGIN_JSON_KEY", "PLUGIN_LOCATION", "PLUGIN_OIDC_TOKEN_ID", "PLUGIN_PASSWORD",
		"PLUGIN_POOL_ID", "PLUGIN_PROJECT_NUMBER", "PLUGIN_PROVIDER_ID", "PLUGIN_REGION", "PLUGIN_REGISTRY",
		"PLUGIN_SESSION_TOKEN",
		"PLUGIN_SERVICE_ACCOUNT_EMAIL", "PLUGIN_TENANT_ID", "PLUGIN_USERNAME", "PLUGIN_WORKLOAD_IDENTITY",
		"SERVICE_PRINCIPAL_CLIENT_ID", "SERVICE_PRINCIPAL_CLIENT_SECRET", "TENANT_ID", "TOKEN",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}
