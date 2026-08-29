package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/harness-community/drone-kimia/internal/envutil"
)

var acrRegistryPattern = regexp.MustCompile(`^[a-z0-9]{5,50}\.azurecr\.io$`)

const (
	acrTokenUsername          = "00000000-0000-0000-0000-000000000000"
	defaultAzureAuthorityHost = "https://login.microsoftonline.com"
	azureManagementScope      = "https://management.azure.com/.default"
)

func resolveACRCredential(ctx context.Context, registry string, client *http.Client) (credential, string, bool, error) {
	registry = canonicalRegistry(registry)
	if registry == "" || registry == "azurecr.io" {
		return credential{}, "", false, fmt.Errorf("PLUGIN_REGISTRY must contain a concrete ACR registry host")
	}
	if !acrRegistryPattern.MatchString(strings.ToLower(registry)) {
		return credential{}, "", false, fmt.Errorf("ACR registry %q is not a supported Azure Container Registry login host", registry)
	}

	directUsername := envutil.First("SERVICE_PRINCIPAL_CLIENT_ID")
	directPassword := envutil.First("SERVICE_PRINCIPAL_CLIENT_SECRET")
	if directUsername != "" || directPassword != "" {
		if directUsername == "" || directPassword == "" {
			return credential{}, "", false, fmt.Errorf("SERVICE_PRINCIPAL_CLIENT_ID and SERVICE_PRINCIPAL_CLIENT_SECRET must both be provided")
		}
		return credential{Registry: registry, Username: directUsername, Password: directPassword}, registry, true, nil
	}
	authority := envutil.First("AZURE_AUTHORITY_HOST", "PLUGIN_AZURE_AUTHORITY_HOST")
	if err := validateAzureAuthority(authority); err != nil {
		return credential{}, "", false, err
	}

	clientID := envutil.First("CLIENT_ID", "AZURE_CLIENT_ID", "AZURE_APP_ID", "PLUGIN_CLIENT_ID")
	tenantID := envutil.First("TENANT_ID", "AZURE_TENANT_ID", "PLUGIN_TENANT_ID")
	oidcToken := envutil.First("PLUGIN_OIDC_TOKEN_ID")
	clientSecret := envutil.First("CLIENT_SECRET", "PLUGIN_CLIENT_SECRET")
	clientCertificate := envutil.First("CLIENT_CERTIFICATE", "PLUGIN_CLIENT_CERTIFICATE")

	var aadToken string
	var err error
	switch {
	case oidcToken != "":
		if clientID == "" || tenantID == "" {
			return credential{}, "", false, fmt.Errorf("ACR OIDC requires client ID and tenant ID")
		}
		aadToken, err = getAADAccessTokenViaClientAssertion(ctx, client, authority, tenantID, clientID, oidcToken)
	case clientSecret != "":
		if clientID == "" || tenantID == "" {
			return credential{}, "", false, fmt.Errorf("ACR client-secret authentication requires client ID and tenant ID")
		}
		var tokenCredential *azidentity.ClientSecretCredential
		tokenCredential, err = azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
		if err == nil {
			aadToken, err = azureAccessToken(ctx, tokenCredential)
		}
	case clientCertificate != "":
		if clientID == "" || tenantID == "" {
			return credential{}, "", false, fmt.Errorf("ACR client-certificate authentication requires client ID and tenant ID")
		}
		aadToken, err = azureCertificateAccessToken(ctx, tenantID, clientID, clientCertificate)
	default:
		options := &azidentity.DefaultAzureCredentialOptions{}
		if tenantID != "" {
			options.TenantID = tenantID
		}
		var tokenCredential *azidentity.DefaultAzureCredential
		tokenCredential, err = azidentity.NewDefaultAzureCredential(options)
		if err == nil {
			aadToken, err = azureAccessToken(ctx, tokenCredential)
		}
	}
	if err != nil {
		return credential{}, "", false, fmt.Errorf("obtain Azure AD access token for ACR: %w", err)
	}
	if tenantID == "" {
		return credential{}, "", false, fmt.Errorf("tenant ID is required for the ACR token exchange")
	}
	refreshToken, err := exchangeACRToken(ctx, client, "https://"+registry+"/oauth2/exchange", tenantID, registry, aadToken)
	if err != nil {
		return credential{}, "", false, err
	}
	return credential{Registry: registry, Username: acrTokenUsername, Password: refreshToken}, registry, true, nil
}

func validateAzureAuthority(authority string) error {
	if strings.TrimSpace(authority) == "" {
		return nil
	}
	parsed, err := url.Parse(authority)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("AZURE_AUTHORITY_HOST must be %s", defaultAzureAuthorityHost)
	}
	if !strings.EqualFold(parsed.Hostname(), "login.microsoftonline.com") {
		return fmt.Errorf("AZURE_AUTHORITY_HOST must be %s for the supported public Azure cloud", defaultAzureAuthorityHost)
	}
	return nil
}

type azureTokenCredential interface {
	GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)
}

func azureAccessToken(ctx context.Context, credential azureTokenCredential) (string, error) {
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureManagementScope}})
	if err != nil {
		return "", err
	}
	if token.Token == "" {
		return "", fmt.Errorf("Azure AD response did not include an access token")
	}
	return token.Token, nil
}

func azureCertificateAccessToken(ctx context.Context, tenantID, clientID, encodedCertificate string) (string, error) {
	certificate, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedCertificate))
	if err != nil {
		return "", fmt.Errorf("decode ACR client certificate: %w", err)
	}
	temporary, err := os.CreateTemp("", "drone-kimia-acr-cert-*.pem")
	if err != nil {
		return "", fmt.Errorf("create temporary ACR certificate: %w", err)
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(certificate); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}

	restore := setTemporaryEnvironment(map[string]string{
		"AZURE_CLIENT_ID":               clientID,
		"AZURE_CLIENT_SECRET":           "",
		"AZURE_TENANT_ID":               tenantID,
		"AZURE_CLIENT_CERTIFICATE_PATH": path,
	})
	defer restore()
	credential, err := azidentity.NewEnvironmentCredential(nil)
	if err != nil {
		return "", err
	}
	return azureAccessToken(ctx, credential)
}

func getAADAccessTokenViaClientAssertion(ctx context.Context, client *http.Client, authority, tenantID, clientID, oidcToken string) (string, error) {
	if strings.TrimSpace(authority) == "" {
		authority = defaultAzureAuthorityHost
	}
	endpoint := strings.TrimRight(authority, "/") + "/" + url.PathEscape(tenantID) + "/oauth2/v2.0/token"
	form := url.Values{
		"client_id":             {clientID},
		"scope":                 {azureManagementScope},
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {oidcToken},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	addUserAgent(request)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("Azure AD token request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("Azure AD response did not include an access token")
	}
	return payload.AccessToken, nil
}

func exchangeACRToken(ctx context.Context, client *http.Client, endpoint, tenantID, registry, aadToken string) (string, error) {
	form := url.Values{
		"grant_type":   {"access_token"},
		"service":      {registry},
		"tenant":       {tenantID},
		"access_token": {aadToken},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create ACR token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	addUserAgent(request)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("exchange Azure AD token for ACR token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("ACR token exchange failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var payload struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode ACR token response: %w", err)
	}
	if payload.RefreshToken == "" {
		return "", fmt.Errorf("ACR token response did not include a refresh token")
	}
	return payload.RefreshToken, nil
}

func setTemporaryEnvironment(values map[string]string) func() {
	type previousValue struct {
		value string
		set   bool
	}
	previous := make(map[string]previousValue, len(values))
	for key, value := range values {
		old, exists := os.LookupEnv(key)
		previous[key] = previousValue{value: old, set: exists}
		_ = os.Setenv(key, value)
	}
	return func() {
		for key, value := range previous {
			if value.set {
				_ = os.Setenv(key, value.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
}
