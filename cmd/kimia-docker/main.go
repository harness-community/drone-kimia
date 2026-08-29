package main

import (
	"os"

	"github.com/harness-community/drone-kimia/internal/plugincli"
	"github.com/urfave/cli"
)

func main() {
	os.Exit(plugincli.Main(plugincli.Options{
		Provider:      "docker",
		ProviderFlags: providerFlags(),
	}))
}

func providerFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:   "docker.registry, registry",
			Usage:  "Docker registry",
			Value:  "https://index.docker.io/v1/",
			EnvVar: "PLUGIN_REGISTRY,DOCKER_REGISTRY",
		},
		cli.StringFlag{
			Name:   "docker.username, username",
			Usage:  "Docker registry username",
			EnvVar: "PLUGIN_USERNAME,DOCKER_USERNAME",
		},
		cli.StringFlag{
			Name:   "docker.password, password",
			Usage:  "Docker registry password",
			EnvVar: "PLUGIN_PASSWORD,DOCKER_PASSWORD",
		},
		cli.StringFlag{
			Name:   "access-token",
			Usage:  "Docker registry OAuth access token",
			EnvVar: "ACCESS_TOKEN",
		},
		cli.StringFlag{
			Name:   "docker.config, dockerconfig",
			Usage:  "Docker config.json content",
			EnvVar: "PLUGIN_CONFIG,DOCKER_PLUGIN_CONFIG",
		},
		cli.StringFlag{
			Name:   "docker.baseimageregistry, base-image-registry",
			Usage:  "Registry used to pull base images",
			EnvVar: "PLUGIN_DOCKER_REGISTRY,PLUGIN_BASE_IMAGE_REGISTRY,DOCKER_BASE_IMAGE_REGISTRY",
		},
		cli.StringFlag{
			Name:   "docker.baseimageusername, base-image-username",
			Usage:  "Username for the base-image registry",
			EnvVar: "PLUGIN_DOCKER_USERNAME,PLUGIN_BASE_IMAGE_USERNAME,DOCKER_BASE_IMAGE_USERNAME",
		},
		cli.StringFlag{
			Name:   "docker.baseimagepassword, base-image-password",
			Usage:  "Password for the base-image registry",
			EnvVar: "PLUGIN_DOCKER_PASSWORD,PLUGIN_BASE_IMAGE_PASSWORD,DOCKER_BASE_IMAGE_PASSWORD",
		},
	}
}
