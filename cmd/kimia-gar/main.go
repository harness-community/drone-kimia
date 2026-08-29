package main

import (
	"os"

	"github.com/harness-community/drone-kimia/internal/plugincli"
	"github.com/urfave/cli"
)

func main() {
	os.Exit(plugincli.Main(plugincli.Options{
		Provider:      "gar",
		ProviderFlags: providerFlags(),
	}))
}

func providerFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:   "registry",
			Usage:  "Google Artifact Registry host",
			EnvVar: "PLUGIN_REGISTRY",
		},
		cli.StringFlag{
			Name:   "location",
			Usage:  "Google Artifact Registry location used to derive the registry host",
			EnvVar: "PLUGIN_LOCATION",
		},
		cli.StringFlag{
			Name:   "json-key",
			Usage:  "Google service-account or workload-identity JSON, raw or base64 encoded",
			EnvVar: "PLUGIN_JSON_KEY,GCR_JSON_KEY,GOOGLE_CREDENTIALS,TOKEN",
		},
		cli.BoolFlag{
			Name:   "workload-identity",
			Usage:  "Exchange workload-identity JSON credentials for an OAuth access token",
			EnvVar: "PLUGIN_WORKLOAD_IDENTITY",
		},
		cli.StringFlag{
			Name:   "oidc-token-id",
			Usage:  "OIDC ID token for Google workload identity federation",
			EnvVar: "PLUGIN_OIDC_TOKEN_ID",
		},
		cli.StringFlag{
			Name:   "project-number",
			Usage:  "Google Cloud project number for workload identity federation",
			EnvVar: "PLUGIN_PROJECT_NUMBER",
		},
		cli.StringFlag{
			Name:   "pool-id",
			Usage:  "Google workload identity pool ID",
			EnvVar: "PLUGIN_POOL_ID",
		},
		cli.StringFlag{
			Name:   "provider-id",
			Usage:  "Google workload identity provider ID",
			EnvVar: "PLUGIN_PROVIDER_ID",
		},
		cli.StringFlag{
			Name:   "service-account-email",
			Usage:  "Google service-account email to impersonate",
			EnvVar: "PLUGIN_SERVICE_ACCOUNT_EMAIL",
		},
		cli.StringFlag{
			Name:   "docker.config, dockerconfig",
			Usage:  "Docker config.json content",
			EnvVar: "PLUGIN_CONFIG,DOCKER_PLUGIN_CONFIG",
		},
		cli.StringFlag{
			Name:   "base-image-registry",
			Usage:  "Registry used to pull base images",
			EnvVar: "PLUGIN_DOCKER_REGISTRY,PLUGIN_BASE_IMAGE_REGISTRY,DOCKER_BASE_IMAGE_REGISTRY,DOCKER_REGISTRY",
		},
		cli.StringFlag{
			Name:   "base-image-username",
			Usage:  "Username for the base-image registry",
			EnvVar: "PLUGIN_DOCKER_USERNAME,PLUGIN_BASE_IMAGE_USERNAME,DOCKER_BASE_IMAGE_USERNAME,DOCKER_USERNAME",
		},
		cli.StringFlag{
			Name:   "base-image-password",
			Usage:  "Password for the base-image registry",
			EnvVar: "PLUGIN_DOCKER_PASSWORD,PLUGIN_BASE_IMAGE_PASSWORD,DOCKER_BASE_IMAGE_PASSWORD,DOCKER_PASSWORD",
		},
	}
}
