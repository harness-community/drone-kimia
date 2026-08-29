package main

import (
	"os"
	"strings"
	"testing"

	"github.com/harness-community/drone-kimia/internal/plugincli"
	"github.com/urfave/cli"
)

func TestProviderFlagsExposeDockerAuthentication(t *testing.T) {
	assertProviderEnvironment(t, providerFlags(), []string{
		"PLUGIN_REGISTRY",
		"DOCKER_REGISTRY",
		"PLUGIN_USERNAME",
		"DOCKER_USERNAME",
		"PLUGIN_PASSWORD",
		"DOCKER_PASSWORD",
		"ACCESS_TOKEN",
		"PLUGIN_CONFIG",
		"DOCKER_PLUGIN_CONFIG",
		"PLUGIN_DOCKER_REGISTRY",
		"PLUGIN_DOCKER_USERNAME",
		"PLUGIN_DOCKER_PASSWORD",
	})
}

func TestKanikoStyleCLIAliasesBindDockerAuthentication(t *testing.T) {
	t.Setenv("PLUGIN_REGISTRY", "")
	t.Setenv("PLUGIN_USERNAME", "")
	t.Setenv("PLUGIN_PASSWORD", "")
	flags := providerFlags()
	application := cli.NewApp()
	application.Flags = flags
	application.Action = func(context *cli.Context) error {
		return plugincli.ApplyContext(context, flags)
	}
	if err := application.Run([]string{
		"kimia-docker",
		"--registry", "registry.example",
		"--username", "connector-user",
		"--password", "connector-password",
	}); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]string{
		"PLUGIN_REGISTRY": "registry.example",
		"PLUGIN_USERNAME": "connector-user",
		"PLUGIN_PASSWORD": "connector-password",
	} {
		if actual := os.Getenv(key); actual != expected {
			t.Errorf("%s = %q, want %q", key, actual, expected)
		}
	}
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
