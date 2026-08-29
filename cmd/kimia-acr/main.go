package main

import (
	"os"

	"github.com/harness-community/drone-kimia/internal/plugincli"
	"github.com/urfave/cli"
)

func main() {
	os.Exit(plugincli.Main(plugincli.Options{
		Provider:      "acr",
		ProviderFlags: providerFlags(),
	}))
}

func providerFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:   "registry",
			Usage:  "Azure Container Registry login host",
			EnvVar: "PLUGIN_REGISTRY",
		},
		cli.StringFlag{
			Name:   "service-principal-client-id",
			Usage:  "Service-principal client ID used directly as the ACR username",
			EnvVar: "SERVICE_PRINCIPAL_CLIENT_ID",
		},
		cli.StringFlag{
			Name:   "service-principal-client-secret",
			Usage:  "Service-principal client secret used directly as the ACR password",
			EnvVar: "SERVICE_PRINCIPAL_CLIENT_SECRET",
		},
		cli.StringFlag{
			Name:   "client-id",
			Usage:  "Azure client ID, also known as the application ID",
			EnvVar: "CLIENT_ID,AZURE_CLIENT_ID,AZURE_APP_ID,PLUGIN_CLIENT_ID",
		},
		cli.StringFlag{
			Name:   "client-secret",
			Usage:  "Azure client secret",
			EnvVar: "CLIENT_SECRET,PLUGIN_CLIENT_SECRET",
		},
		cli.StringFlag{
			Name:   "client-cert",
			Usage:  "Base64-encoded Azure client certificate",
			EnvVar: "CLIENT_CERTIFICATE,PLUGIN_CLIENT_CERTIFICATE",
		},
		cli.StringFlag{
			Name:   "tenant-id",
			Usage:  "Azure tenant ID",
			EnvVar: "TENANT_ID,AZURE_TENANT_ID,PLUGIN_TENANT_ID",
		},
		cli.StringFlag{
			Name:   "oidc-token-id",
			Usage:  "OIDC ID token to exchange for an Azure AD access token",
			EnvVar: "PLUGIN_OIDC_TOKEN_ID",
		},
		cli.StringFlag{
			Name:   "azure-authority-host",
			Usage:  "Azure authority host",
			EnvVar: "AZURE_AUTHORITY_HOST,PLUGIN_AZURE_AUTHORITY_HOST",
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
