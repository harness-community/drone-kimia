package main

import (
	"os"

	"github.com/harness-community/drone-kimia/internal/plugincli"
	"github.com/urfave/cli"
)

func main() {
	os.Exit(plugincli.Main(plugincli.Options{
		Provider:      "ecr",
		ProviderFlags: providerFlags(),
	}))
}

func providerFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:   "registry",
			Usage:  "Amazon Elastic Container Registry host",
			EnvVar: "PLUGIN_REGISTRY",
		},
		cli.StringFlag{
			Name:   "region",
			Usage:  "AWS region",
			Value:  "us-east-1",
			EnvVar: "PLUGIN_REGION,ECR_REGION,AWS_REGION",
		},
		cli.StringFlag{
			Name:   "access-key",
			Usage:  "AWS access key ID",
			EnvVar: "PLUGIN_ACCESS_KEY,ECR_ACCESS_KEY,AWS_ACCESS_KEY_ID",
		},
		cli.StringFlag{
			Name:   "secret-key",
			Usage:  "AWS secret access key",
			EnvVar: "PLUGIN_SECRET_KEY,ECR_SECRET_KEY,AWS_SECRET_ACCESS_KEY",
		},
		cli.StringFlag{
			Name:   "session-token",
			Usage:  "AWS session token for temporary credentials",
			EnvVar: "AWS_SESSION_TOKEN",
		},
		cli.StringFlag{
			Name:   "assume-role",
			Usage:  "AWS role ARN to assume",
			EnvVar: "PLUGIN_ASSUME_ROLE",
		},
		cli.StringFlag{
			Name:   "external-id",
			Usage:  "External ID used when assuming an AWS role",
			EnvVar: "PLUGIN_EXTERNAL_ID",
		},
		cli.StringFlag{
			Name:   "oidc-token-id",
			Usage:  "OIDC ID token used to assume an AWS role with web identity",
			EnvVar: "PLUGIN_OIDC_TOKEN_ID",
		},
		cli.StringFlag{
			Name:   "docker.config, dockerconfig",
			Usage:  "Docker config.json content",
			EnvVar: "PLUGIN_CONFIG,DOCKER_PLUGIN_CONFIG",
		},
		cli.StringFlag{
			Name:   "docker-registry",
			Usage:  "Registry used to pull base images",
			EnvVar: "PLUGIN_DOCKER_REGISTRY,PLUGIN_BASE_IMAGE_REGISTRY,DOCKER_BASE_IMAGE_REGISTRY,DOCKER_REGISTRY",
		},
		cli.StringFlag{
			Name:   "docker-username",
			Usage:  "Username for the base-image registry",
			EnvVar: "PLUGIN_DOCKER_USERNAME,PLUGIN_BASE_IMAGE_USERNAME,DOCKER_BASE_IMAGE_USERNAME,DOCKER_USERNAME,PLUGIN_USERNAME",
		},
		cli.StringFlag{
			Name:   "docker-password",
			Usage:  "Password for the base-image registry",
			EnvVar: "PLUGIN_DOCKER_PASSWORD,PLUGIN_BASE_IMAGE_PASSWORD,DOCKER_BASE_IMAGE_PASSWORD,DOCKER_PASSWORD,PLUGIN_PASSWORD",
		},
	}
}
