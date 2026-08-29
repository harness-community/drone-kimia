package main

import (
	"strings"
	"testing"

	"github.com/urfave/cli"
)

func TestProviderFlagsExposeACRAuthentication(t *testing.T) {
	assertProviderEnvironment(t, providerFlags(), []string{
		"PLUGIN_REGISTRY",
		"SERVICE_PRINCIPAL_CLIENT_ID",
		"SERVICE_PRINCIPAL_CLIENT_SECRET",
		"CLIENT_ID",
		"AZURE_CLIENT_ID",
		"AZURE_APP_ID",
		"PLUGIN_CLIENT_ID",
		"CLIENT_SECRET",
		"PLUGIN_CLIENT_SECRET",
		"CLIENT_CERTIFICATE",
		"PLUGIN_CLIENT_CERTIFICATE",
		"TENANT_ID",
		"AZURE_TENANT_ID",
		"PLUGIN_TENANT_ID",
		"AZURE_AUTHORITY_HOST",
		"PLUGIN_AZURE_AUTHORITY_HOST",
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
