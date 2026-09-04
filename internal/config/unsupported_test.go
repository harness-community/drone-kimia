package config

import (
	"strings"
	"testing"
)

func TestSnapshotModeRedoIsAcceptedAsCompatibilityNoOp(t *testing.T) {
	t.Setenv("PLUGIN_SNAPSHOT_MODE", "redo")
	if err := ValidateUnsupportedEnvironment(); err != nil {
		t.Fatalf("ValidateUnsupportedEnvironment() error = %v", err)
	}
}

func TestOtherSnapshotModesAreRejected(t *testing.T) {
	for _, value := range []string{"full", "time", "false"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PLUGIN_SNAPSHOT_MODE", value)
			err := ValidateUnsupportedEnvironment()
			if err == nil {
				t.Fatalf("ValidateUnsupportedEnvironment() accepted PLUGIN_SNAPSHOT_MODE=%q", value)
			}
			if !strings.Contains(err.Error(), "PLUGIN_SNAPSHOT_MODE") || !strings.Contains(err.Error(), "redo") {
				t.Fatalf("ValidateUnsupportedEnvironment() error = %q, want snapshot-mode guidance", err)
			}
		})
	}
}

func TestMetadataFileIsIgnoredForHarnessCompatibility(t *testing.T) {
	t.Setenv("PLUGIN_METADATA_FILE", "/harness/buildx-metadata.json")
	if err := ValidateUnsupportedEnvironment(); err != nil {
		t.Fatalf("ValidateUnsupportedEnvironment() error = %v", err)
	}
}

func TestDaemonOffTrueIsAcceptedAsCompatibilityNoOp(t *testing.T) {
	t.Setenv("PLUGIN_DAEMON_OFF", "true")
	if err := ValidateUnsupportedEnvironment(); err != nil {
		t.Fatalf("ValidateUnsupportedEnvironment() error = %v", err)
	}
}

func TestDaemonOffFalseIsRejected(t *testing.T) {
	t.Setenv("PLUGIN_DAEMON_OFF", "false")
	err := ValidateUnsupportedEnvironment()
	if err == nil {
		t.Fatal("ValidateUnsupportedEnvironment() accepted PLUGIN_DAEMON_OFF=false")
	}
	if !strings.Contains(err.Error(), "always daemonless") {
		t.Fatalf("ValidateUnsupportedEnvironment() error = %q, want daemonless guidance", err)
	}
}

func TestDaemonOffMustBeBoolean(t *testing.T) {
	t.Setenv("PLUGIN_DAEMON_OFF", "sometimes")
	err := ValidateUnsupportedEnvironment()
	if err == nil {
		t.Fatal("ValidateUnsupportedEnvironment() accepted a non-boolean PLUGIN_DAEMON_OFF")
	}
	if !strings.Contains(err.Error(), "must be a boolean") {
		t.Fatalf("ValidateUnsupportedEnvironment() error = %q, want boolean guidance", err)
	}
}

func TestBuildahInputsAreNotRejectedAsUnsupported(t *testing.T) {
	t.Setenv("PLUGIN_STORAGE_DRIVER", "vfs")
	t.Setenv("PLUGIN_INSECURE_PULL", "true")
	t.Setenv("PLUGIN_IMAGE_DOWNLOAD_RETRY", "2")
	t.Setenv("PLUGIN_PUSH_RETRY", "3")
	t.Setenv("PLUGIN_BUILDAH_OPT", "--squash")
	if err := ValidateUnsupportedEnvironment(); err != nil {
		t.Fatalf("ValidateUnsupportedEnvironment() rejected Buildah inputs: %v", err)
	}
}
