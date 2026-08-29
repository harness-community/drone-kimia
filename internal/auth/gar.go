package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/harness-community/drone-kimia/internal/envutil"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var garRegistryPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?-docker\.pkg\.dev$`)

const (
	googleCloudScope       = "https://www.googleapis.com/auth/cloud-platform"
	googleSTSEndpoint      = "https://sts.googleapis.com/v1/token"
	googleIAMEndpoint      = "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/"
	googleTokenGrantType   = "urn:ietf:params:oauth:grant-type:token-exchange"
	googleAccessTokenType  = "urn:ietf:params:oauth:token-type:access_token"
	googleSubjectTokenType = "urn:ietf:params:oauth:token-type:id_token"
)

func resolveGARCredential(ctx context.Context, registry string, client *http.Client) (credential, string, bool, error) {
	registry = garRegistry(registry)
	if registry == "" {
		return credential{}, "", false, fmt.Errorf("PLUGIN_REGISTRY or PLUGIN_LOCATION is required for GAR")
	}
	if !garRegistryPattern.MatchString(strings.ToLower(registry)) {
		return credential{}, "", false, fmt.Errorf("GAR registry %q must be a Google Artifact Registry host ending in -docker.pkg.dev", registry)
	}

	oidcToken := envutil.First("PLUGIN_OIDC_TOKEN_ID")
	projectNumber := envutil.First("PLUGIN_PROJECT_NUMBER")
	poolID := envutil.First("PLUGIN_POOL_ID")
	providerID := envutil.First("PLUGIN_PROVIDER_ID")
	serviceAccount := envutil.First("PLUGIN_SERVICE_ACCOUNT_EMAIL")
	oidcValues := []string{oidcToken, projectNumber, poolID, providerID, serviceAccount}
	if anyNonEmpty(oidcValues) {
		if !allNonEmpty(oidcValues) {
			return credential{}, "", false, fmt.Errorf("GAR OIDC requires PLUGIN_OIDC_TOKEN_ID, PLUGIN_PROJECT_NUMBER, PLUGIN_POOL_ID, PLUGIN_PROVIDER_ID, and PLUGIN_SERVICE_ACCOUNT_EMAIL")
		}
		accessToken, err := exchangeGoogleOIDC(
			ctx,
			client,
			googleSTSEndpoint,
			googleIAMEndpoint,
			oidcToken,
			projectNumber,
			poolID,
			providerID,
			serviceAccount,
		)
		if err != nil {
			return credential{}, "", false, err
		}
		return credential{Registry: registry, Username: "oauth2accesstoken", Password: accessToken}, registry, true, nil
	}

	jsonKey := envutil.First("PLUGIN_JSON_KEY", "GCR_JSON_KEY", "GOOGLE_CREDENTIALS", "TOKEN")
	if jsonKey == "" {
		// Keep the existing Docker config and ambient Google credentials intact.
		// Kimia's bundled Google credential helper can use them.
		return credential{}, registry, false, nil
	}
	decodedKey := decodeGoogleCredential(jsonKey)
	workloadIdentity, err := envutil.Bool("PLUGIN_WORKLOAD_IDENTITY")
	if err != nil {
		return credential{}, "", false, err
	}
	if !workloadIdentity {
		return credential{Registry: registry, Username: "_json_key", Password: decodedKey}, registry, true, nil
	}

	tokenContext := context.WithValue(ctx, oauth2.HTTPClient, client)
	credentials, err := google.CredentialsFromJSON(tokenContext, []byte(decodedKey), googleCloudScope)
	if err != nil {
		return credential{}, "", false, fmt.Errorf("parse GAR workload identity credentials: %w", err)
	}
	token, err := credentials.TokenSource.Token()
	if err != nil {
		return credential{}, "", false, fmt.Errorf("obtain GAR OAuth access token: %w", err)
	}
	if token == nil || token.AccessToken == "" {
		return credential{}, "", false, fmt.Errorf("GAR OAuth response did not include an access token")
	}
	return credential{Registry: registry, Username: "oauth2accesstoken", Password: token.AccessToken}, registry, true, nil
}

func garRegistry(registry string) string {
	if strings.TrimSpace(registry) != "" {
		return canonicalRegistry(registry)
	}
	location := strings.TrimSpace(envutil.First("PLUGIN_LOCATION"))
	if location == "" {
		return ""
	}
	return location + "-docker.pkg.dev"
}

func decodeGoogleCredential(value string) string {
	trimmed := strings.TrimSpace(value)
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err == nil && json.Valid(decoded) {
		return string(decoded)
	}
	return value
}

func exchangeGoogleOIDC(
	ctx context.Context,
	client *http.Client,
	stsEndpoint string,
	iamEndpoint string,
	idToken string,
	projectNumber string,
	poolID string,
	providerID string,
	serviceAccount string,
) (string, error) {
	audience := fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s", projectNumber, poolID, providerID)
	requestBody := map[string]string{
		"grantType":          googleTokenGrantType,
		"subjectToken":       idToken,
		"audience":           audience,
		"scope":              googleCloudScope,
		"requestedTokenType": googleAccessTokenType,
		"subjectTokenType":   googleSubjectTokenType,
	}
	var stsResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := postJSON(ctx, client, stsEndpoint, "", requestBody, &stsResponse); err != nil {
		return "", fmt.Errorf("exchange GAR OIDC token with Google STS: %w", err)
	}
	if stsResponse.AccessToken == "" {
		return "", fmt.Errorf("Google STS response did not include an access token")
	}

	serviceAccountPath := url.PathEscape(serviceAccount)
	endpoint := strings.TrimRight(iamEndpoint, "/") + "/" + serviceAccountPath + ":generateAccessToken"
	iamRequest := map[string]any{"scope": []string{googleCloudScope}}
	var iamResponse struct {
		AccessToken string `json:"accessToken"`
	}
	if err := postJSON(ctx, client, endpoint, stsResponse.AccessToken, iamRequest, &iamResponse); err != nil {
		return "", fmt.Errorf("generate GAR service-account access token: %w", err)
	}
	if iamResponse.AccessToken == "" {
		return "", fmt.Errorf("Google IAM Credentials response did not include an access token")
	}
	return iamResponse.AccessToken, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint, bearer string, input, output any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	addUserAgent(request)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return err
	}
	return nil
}

func anyNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func allNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}
