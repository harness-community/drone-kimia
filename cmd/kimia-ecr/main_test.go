package main

import (
	"strings"
	"testing"

	"github.com/urfave/cli"
)

func TestProviderFlagsExposeECRAuthentication(t *testing.T) {
	assertProviderEnvironment(t, providerFlags(), []string{
		"PLUGIN_REGISTRY",
		"PLUGIN_REGION",
		"ECR_REGION",
		"AWS_REGION",
		"PLUGIN_ACCESS_KEY",
		"ECR_ACCESS_KEY",
		"AWS_ACCESS_KEY_ID",
		"PLUGIN_SECRET_KEY",
		"ECR_SECRET_KEY",
		"AWS_SECRET_ACCESS_KEY",
		"PLUGIN_SESSION_TOKEN",
		"AWS_SESSION_TOKEN",
		"PLUGIN_ASSUME_ROLE",
		"PLUGIN_EXTERNAL_ID",
		"PLUGIN_OIDC_TOKEN_ID",
		"PLUGIN_DOCKER_REGISTRY",
		"PLUGIN_DOCKER_USERNAME",
		"PLUGIN_DOCKER_PASSWORD",
	})
}

func assertProviderEnvironment(t *testing.T, flags []cli.Flag, expected []string) {
	t.Helper()
	available := make(map[string]bool)
	for _, flag := range flags {
		if value := providerFlagEnvironment(flag); value != "" {
			for _, key := range strings.Split(value, ",") {
				available[strings.TrimSpace(key)] = true
			}
		}
	}
	for _, key := range expected {
		if !available[key] {
			t.Errorf("provider flags do not expose %s", key)
		}
	}
}

func providerFlagEnvironment(flag cli.Flag) string {
	switch typed := flag.(type) {
	case cli.StringFlag:
		return typed.EnvVar
	case cli.BoolFlag:
		return typed.EnvVar
	default:
		return ""
	}
}
