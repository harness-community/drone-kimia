package main

import (
	"strings"
	"testing"

	"github.com/urfave/cli"
)

func TestProviderFlagsExposeGARAuthentication(t *testing.T) {
	assertProviderEnvironment(t, providerFlags(), []string{
		"PLUGIN_REGISTRY",
		"PLUGIN_LOCATION",
		"PLUGIN_JSON_KEY",
		"GCR_JSON_KEY",
		"GOOGLE_CREDENTIALS",
		"TOKEN",
		"PLUGIN_WORKLOAD_IDENTITY",
		"PLUGIN_OIDC_TOKEN_ID",
		"PLUGIN_PROJECT_NUMBER",
		"PLUGIN_POOL_ID",
		"PLUGIN_PROVIDER_ID",
		"PLUGIN_SERVICE_ACCOUNT_EMAIL",
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
