package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/harness-community/drone-kimia/internal/envutil"
)

const defaultAWSRegion = "us-east-1"

var ecrRegistryPattern = regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr(?:-fips)?\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$`)

func resolveECRCredential(ctx context.Context, registry string, suppliedClient ECRClient) (credential, string, bool, error) {
	registry = canonicalRegistry(registry)
	registryRegion := ""
	if registry != "" {
		var err error
		registryRegion, err = privateECRRegistryRegion(registry)
		if err != nil {
			return credential{}, "", false, err
		}
	}
	configuredRegion := envutil.First("PLUGIN_REGION", "ECR_REGION", "AWS_REGION")
	if configuredRegion != "" && registryRegion != "" && !strings.EqualFold(configuredRegion, registryRegion) {
		return credential{}, "", false, fmt.Errorf("ECR registry %q is in region %s but the configured region is %s", registry, registryRegion, configuredRegion)
	}
	region := configuredRegion
	if region == "" {
		region = registryRegion
	}
	if region == "" {
		region = defaultAWSRegion
	}

	client := suppliedClient
	clientConstructed := false
	if client == nil {
		accessKey := envutil.First("PLUGIN_ACCESS_KEY", "ECR_ACCESS_KEY", "AWS_ACCESS_KEY_ID")
		secretKey := envutil.First("PLUGIN_SECRET_KEY", "ECR_SECRET_KEY", "AWS_SECRET_ACCESS_KEY")
		if (accessKey == "") != (secretKey == "") {
			return credential{}, "", false, fmt.Errorf("both ECR access key and secret key must be provided")
		}

		loadOptions := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
		if accessKey != "" {
			provider := credentials.NewStaticCredentialsProvider(accessKey, secretKey, ecrSessionToken())
			loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(provider))
		}
		configuration, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
		if err != nil {
			return credential{}, "", false, fmt.Errorf("load AWS configuration for ECR: %w", err)
		}

		role := envutil.First("PLUGIN_ASSUME_ROLE")
		if role != "" {
			stsClient := sts.NewFromConfig(configuration)
			oidcToken := envutil.First("PLUGIN_OIDC_TOKEN_ID")
			if oidcToken != "" {
				provider := stscreds.NewWebIdentityRoleProvider(stsClient, role, identityToken(oidcToken))
				configuration.Credentials = aws.NewCredentialsCache(provider)
			} else {
				externalID := envutil.First("PLUGIN_EXTERNAL_ID")
				provider := stscreds.NewAssumeRoleProvider(stsClient, role, func(options *stscreds.AssumeRoleOptions) {
					if externalID != "" {
						options.ExternalID = aws.String(externalID)
					}
				})
				configuration.Credentials = aws.NewCredentialsCache(provider)
			}
		}
		client = ecr.NewFromConfig(configuration)
		clientConstructed = true
	}

	output, err := client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return credential{}, "", false, fmt.Errorf("get ECR authorization token: %w", err)
	}
	username, password, tokenRegistry, err := decodeECRAuthorization(output)
	if err != nil {
		return credential{}, "", false, err
	}
	tokenRegion, err := privateECRRegistryRegion(tokenRegistry)
	if err != nil {
		return credential{}, "", false, fmt.Errorf("ECR returned unsupported registry host %q: %w", tokenRegistry, err)
	}
	expectedRegion := registryRegion
	if expectedRegion == "" && (clientConstructed || configuredRegion != "") {
		expectedRegion = region
	}
	if expectedRegion != "" && !strings.EqualFold(expectedRegion, tokenRegion) {
		return credential{}, "", false, fmt.Errorf("ECR authorization token is for region %s, expected %s", tokenRegion, expectedRegion)
	}
	resolvedRegistry := registry
	if resolvedRegistry == "" {
		resolvedRegistry = tokenRegistry
	}
	return credential{Registry: resolvedRegistry, Username: username, Password: password}, resolvedRegistry, true, nil
}

func ecrSessionToken() string {
	return envutil.First("PLUGIN_SESSION_TOKEN", "AWS_SESSION_TOKEN")
}

func privateECRRegistryRegion(registry string) (string, error) {
	registry = strings.ToLower(canonicalRegistry(registry))
	matches := ecrRegistryPattern.FindStringSubmatch(registry)
	if len(matches) != 2 || matches[1] == "" {
		return "", fmt.Errorf("ECR registry %q is not a supported private ECR registry host", registry)
	}
	return matches[1], nil
}

func decodeECRAuthorization(output *ecr.GetAuthorizationTokenOutput) (string, string, string, error) {
	if output == nil || len(output.AuthorizationData) == 0 {
		return "", "", "", fmt.Errorf("ECR returned no authorization data")
	}
	authorization := output.AuthorizationData[0]
	if authorization.AuthorizationToken == nil || *authorization.AuthorizationToken == "" {
		return "", "", "", fmt.Errorf("ECR authorization data did not include a token")
	}
	if authorization.ProxyEndpoint == nil || *authorization.ProxyEndpoint == "" {
		return "", "", "", fmt.Errorf("ECR authorization data did not include a registry endpoint")
	}
	decoded, err := base64.StdEncoding.DecodeString(*authorization.AuthorizationToken)
	if err != nil {
		return "", "", "", fmt.Errorf("decode ECR authorization token: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("ECR authorization token has an invalid username/password format")
	}
	return parts[0], parts[1], canonicalRegistry(*authorization.ProxyEndpoint), nil
}

type identityToken string

func (token identityToken) GetIdentityToken() ([]byte, error) {
	return []byte(token), nil
}
