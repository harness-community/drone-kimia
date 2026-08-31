package workspacecompat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareProxiesHarnessWorkspaceAndRewritesAbsoluteDockerfile(t *testing.T) {
	home, workspace := testLayout(t)
	dockerfile := filepath.Join(workspace, "docker", "Dockerfile")
	writeFile(t, dockerfile, "FROM scratch\n")
	t.Chdir(workspace)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", "")

	plan, err := Prepare(Input{
		Context:    ".",
		Dockerfile: dockerfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := plan.privateRoot
	t.Cleanup(func() { _ = plan.Cleanup() })

	if plan.Context == workspace || !strings.HasPrefix(plan.Context, home+string(filepath.Separator)) {
		t.Fatalf("Context = %q, want a private proxy under %q", plan.Context, home)
	}
	if target, err := os.Readlink(plan.Context); err != nil {
		t.Fatalf("Context proxy is not a symlink: %v", err)
	} else if target != workspace {
		t.Fatalf("Context proxy target = %q, want %q", target, workspace)
	}
	if want := filepath.Join("docker", "Dockerfile"); plan.Dockerfile != want {
		t.Fatalf("Dockerfile = %q, want %q", plan.Dockerfile, want)
	}

	if err := plan.Finalize(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(privateRoot); !os.IsNotExist(err) {
		t.Fatalf("private root still exists after Finalize: %v", err)
	}
}

func TestPrepareUsesDroneWorkspace(t *testing.T) {
	home, workspace := testLayout(t)
	contextPath := filepath.Join(workspace, "service")
	mkdir(t, contextPath)
	t.Chdir(workspace)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", "")
	t.Setenv("DRONE_WORKSPACE", workspace)

	plan, err := Prepare(Input{Context: contextPath, Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if target, err := os.Readlink(plan.Context); err != nil {
		t.Fatal(err)
	} else if target != contextPath {
		t.Fatalf("proxy target = %q, want %q", target, contextPath)
	}
}

func TestPrepareUsesValidWorkspaceWhenSecondaryWorkspaceIsMissing(t *testing.T) {
	home, workspace := testLayout(t)
	t.Chdir(workspace)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", filepath.Join(workspace, "missing"))

	plan, err := Prepare(Input{Context: ".", Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if target, err := os.Readlink(plan.Context); err != nil {
		t.Fatal(err)
	} else if target != workspace {
		t.Fatalf("proxy target = %q, want %q", target, workspace)
	}
}

func TestPrepareFallsBackToStartupWorkingDirectory(t *testing.T) {
	home, workspace := testLayout(t)
	t.Chdir(workspace)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", "")
	t.Setenv("DRONE_WORKSPACE", "")

	plan, err := Prepare(Input{Context: ".", Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if target, err := os.Readlink(plan.Context); err != nil {
		t.Fatal(err)
	} else if target != workspace {
		t.Fatalf("proxy target = %q, want %q", target, workspace)
	}
}

func TestPrepareRejectsContextOutsideConfiguredWorkspace(t *testing.T) {
	home, workspace := testLayout(t)
	outside := filepath.Join(t.TempDir(), "outside")
	mkdir(t, outside)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", "")

	_, err := Prepare(Input{Context: outside, Dockerfile: "Dockerfile"})
	if err == nil || !strings.Contains(err.Error(), "outside HOME") {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertNoPrivatePaths(t, home)
}

func TestPrepareRejectsWorkspaceRootSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges on Windows")
	}
	home, workspace := testLayout(t)
	outside := filepath.Join(t.TempDir(), "outside")
	mkdir(t, outside)
	escape := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", "")

	_, err := Prepare(Input{Context: escape, Dockerfile: "Dockerfile"})
	if err == nil || !strings.Contains(err.Error(), "outside HOME") {
		t.Fatalf("Prepare() error = %v", err)
	}
}

func TestPrepareLeavesRemoteContextUnchanged(t *testing.T) {
	home, _ := testLayout(t)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", "")
	t.Setenv("DRONE_WORKSPACE", "")
	const contextURL = "https://example.invalid/team/repo.git"

	plan, err := Prepare(Input{Context: contextURL, Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.Context != contextURL {
		t.Fatalf("Context = %q, want %q", plan.Context, contextURL)
	}
	if plan.privateRoot != "" {
		t.Fatalf("remote context unexpectedly allocated %q", plan.privateRoot)
	}
}

func TestPrepareUsesAbsoluteContextAlreadyWithinHome(t *testing.T) {
	home, _ := testLayout(t)
	contextPath := filepath.Join(home, "workspace")
	mkdir(t, contextPath)
	t.Setenv("HOME", home)

	plan, err := Prepare(Input{Context: contextPath, Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.Context != contextPath {
		t.Fatalf("Context = %q, want %q", plan.Context, contextPath)
	}
	if plan.privateRoot != "" {
		t.Fatalf("home context unexpectedly allocated %q", plan.privateRoot)
	}
}

func TestPrepareRejectsAbsoluteDockerfileOutsideContext(t *testing.T) {
	home, workspace := testLayout(t)
	contextPath := filepath.Join(workspace, "context")
	mkdir(t, contextPath)
	dockerfile := filepath.Join(workspace, "Dockerfile")
	writeFile(t, dockerfile, "FROM scratch\n")
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)

	_, err := Prepare(Input{Context: contextPath, Dockerfile: dockerfile})
	if err == nil || !strings.Contains(err.Error(), "outside build context") {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertNoPrivatePaths(t, home)
}

func TestRelativeTarIsStagedAndPublishedToStartupWorkingDirectory(t *testing.T) {
	home, workspace := testLayout(t)
	t.Chdir(workspace)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	const original = "output/imageci.tar"

	plan, err := Prepare(Input{
		Context:    "https://example.invalid/repo.git",
		Dockerfile: "Dockerfile",
		TarPath:    original,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := plan.privateRoot
	if plan.OriginalTarPath != original {
		t.Fatalf("OriginalTarPath = %q, want %q", plan.OriginalTarPath, original)
	}
	if !filepath.IsAbs(plan.TarPath) || !strings.HasPrefix(plan.TarPath, home+string(filepath.Separator)) {
		t.Fatalf("TarPath = %q, want absolute internal path under %q", plan.TarPath, home)
	}
	writeFile(t, plan.TarPath, "image archive")

	if err := plan.Finalize(true); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(workspace, original), "image archive")
	if _, err := os.Stat(privateRoot); !os.IsNotExist(err) {
		t.Fatalf("private root still exists after Finalize: %v", err)
	}
}

func TestOutsideHomeAbsoluteTarIsStagedAndPublished(t *testing.T) {
	home, _ := testLayout(t)
	destination := filepath.Join(t.TempDir(), "shared", "image.tar")
	t.Setenv("HOME", home)

	plan, err := Prepare(Input{
		Context:    "git://example.invalid/repo.git",
		Dockerfile: "Dockerfile",
		TarPath:    destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TarPath == destination {
		t.Fatalf("TarPath was not staged: %q", plan.TarPath)
	}
	writeFile(t, plan.TarPath, "new archive")
	writeFile(t, destination, "old archive")

	if err := plan.Finalize(true); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, destination, "new archive")
}

func TestFinalizeFailureDoesNotPublishTarAndCleansPrivatePaths(t *testing.T) {
	home, workspace := testLayout(t)
	t.Chdir(workspace)
	t.Setenv("HOME", home)

	plan, err := Prepare(Input{
		Context:    "https://example.invalid/repo.git",
		Dockerfile: "Dockerfile",
		TarPath:    "imageci.tar",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := plan.privateRoot
	writeFile(t, plan.TarPath, "partial archive")
	if err := plan.Finalize(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "imageci.tar")); !os.IsNotExist(err) {
		t.Fatalf("failed build published tar: %v", err)
	}
	if _, err := os.Stat(privateRoot); !os.IsNotExist(err) {
		t.Fatalf("private root still exists after failed Finalize: %v", err)
	}
}

func TestAbsoluteTarWithinHomeIsPassedThrough(t *testing.T) {
	home, _ := testLayout(t)
	destination := filepath.Join(home, "output", "image.tar")
	t.Setenv("HOME", home)

	plan, err := Prepare(Input{
		Context:    "ssh://example.invalid/repo.git",
		Dockerfile: "Dockerfile",
		TarPath:    destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TarPath != destination {
		t.Fatalf("TarPath = %q, want %q", plan.TarPath, destination)
	}
	if plan.OriginalTarPath != destination {
		t.Fatalf("OriginalTarPath = %q, want %q", plan.OriginalTarPath, destination)
	}
	if _, err := os.Stat(filepath.Dir(destination)); err != nil {
		t.Fatalf("tar directory was not created: %v", err)
	}
	writeFile(t, destination, "archive")
	if err := plan.Finalize(true); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, destination, "archive")
}

func TestFinalizeReportsMissingStagedTarAndCleans(t *testing.T) {
	home, workspace := testLayout(t)
	t.Chdir(workspace)
	t.Setenv("HOME", home)

	plan, err := Prepare(Input{
		Context:    "https://example.invalid/repo.git",
		Dockerfile: "Dockerfile",
		TarPath:    "image.tar",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := plan.privateRoot
	err = plan.Finalize(true)
	if err == nil || !strings.Contains(err.Error(), "open staged image tar") {
		t.Fatalf("Finalize() error = %v", err)
	}
	if _, statErr := os.Stat(privateRoot); !os.IsNotExist(statErr) {
		t.Fatalf("private root still exists after Finalize error: %v", statErr)
	}
}

func TestCleanupAndFinalizeAreIdempotent(t *testing.T) {
	home, workspace := testLayout(t)
	t.Chdir(workspace)
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", "")

	plan, err := Prepare(Input{
		Context:    ".",
		Dockerfile: "Dockerfile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Finalize(false); err != nil {
		t.Fatal(err)
	}
	if err := plan.Finalize(true); err != nil {
		t.Fatal(err)
	}
}

func testLayout(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home", "kimia")
	workspace := filepath.Join(root, "harness")
	mkdir(t, home)
	mkdir(t, workspace)
	return home, workspace
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, data, want)
	}
}

func assertNoPrivatePaths(t *testing.T, home string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(home, ".drone-kimia-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("private paths were not cleaned: %v", matches)
	}
}
