// Package workspacecompat adapts ordinary Harness/Drone workspace paths to
// the local-context and tar-output restrictions imposed by RapidFort Kimia.
package workspacecompat

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const defaultKimiaHome = "/home/kimia"

// Input contains the user-visible paths accepted by the plugin.
type Input struct {
	Context    string
	Dockerfile string
	TarPath    string
}

// Plan contains paths that are safe to pass to Kimia. OriginalTarPath retains
// the exact plugin input so callers can expose the user-visible path in plugin
// output after Finalize copies a staged archive back to the workspace.
type Plan struct {
	Context         string
	Dockerfile      string
	TarPath         string
	OriginalTarPath string

	home           string
	workingDir     string
	privateRoot    string
	tarDestination string
	finalized      bool
	cleaned        bool
}

// Prepare resolves paths relative to the process's startup working directory.
// Local contexts outside Kimia's home are exposed through a private symlink
// only when they are contained by a configured Harness/Drone workspace. If no
// workspace variable is set, the startup working directory is the allowed
// workspace root.
func Prepare(input Input) (*Plan, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve startup working directory: %w", err)
	}
	workingDir, err = filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute startup working directory: %w", err)
	}

	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = defaultKimiaHome
	}
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("HOME must be an absolute path, got %q", home)
	}
	home = filepath.Clean(home)
	if err := ensureDirectory(home); err != nil {
		return nil, fmt.Errorf("validate Kimia home %q: %w", home, err)
	}

	plan := &Plan{
		Context:         input.Context,
		Dockerfile:      input.Dockerfile,
		TarPath:         input.TarPath,
		OriginalTarPath: input.TarPath,
		home:            home,
		workingDir:      workingDir,
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = plan.Cleanup()
		}
	}()

	contextSource, local, err := plan.prepareContext(input.Context)
	if err != nil {
		return nil, err
	}
	if local && filepath.IsAbs(input.Dockerfile) {
		plan.Dockerfile, err = relativeDockerfile(contextSource, input.Dockerfile)
		if err != nil {
			return nil, err
		}
	}

	if err := plan.prepareTarPath(input.TarPath); err != nil {
		return nil, err
	}

	prepared = true
	return plan, nil
}

// Finalize publishes a staged tar archive only after a successful build and
// always removes the plan's private paths. It is safe to call more than once;
// only the first call has an effect.
func (p *Plan) Finalize(success bool) error {
	if p == nil || p.finalized {
		return nil
	}
	p.finalized = true

	var publishErr error
	if success && p.tarDestination != "" {
		if err := atomicCopy(p.TarPath, p.tarDestination); err != nil {
			publishErr = fmt.Errorf("publish image tar to %q: %w", p.tarDestination, err)
		}
	}
	cleanupErr := p.Cleanup()
	return errors.Join(publishErr, cleanupErr)
}

// Cleanup removes private context proxies and staged output. It is idempotent
// and should be deferred by callers in case execution stops before Finalize.
func (p *Plan) Cleanup() error {
	if p == nil || p.cleaned {
		return nil
	}
	p.cleaned = true
	if p.privateRoot == "" {
		return nil
	}
	root := p.privateRoot
	p.privateRoot = ""
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove private workspace compatibility directory %q: %w", root, err)
	}
	return nil
}

func (p *Plan) prepareContext(value string) (string, bool, error) {
	if value == "" || isRemoteContext(value) {
		return "", false, nil
	}

	contextPath, err := absoluteFrom(p.workingDir, value)
	if err != nil {
		return "", false, fmt.Errorf("resolve build context %q: %w", value, err)
	}
	info, err := os.Stat(contextPath)
	if err != nil {
		return "", false, fmt.Errorf("inspect build context %q: %w", contextPath, err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("build context %q is not a directory", contextPath)
	}
	contextReal, err := filepath.EvalSymlinks(contextPath)
	if err != nil {
		return "", false, fmt.Errorf("resolve build context %q: %w", contextPath, err)
	}
	contextReal, err = filepath.Abs(contextReal)
	if err != nil {
		return "", false, fmt.Errorf("resolve absolute build context %q: %w", contextPath, err)
	}

	homeReal, err := filepath.EvalSymlinks(p.home)
	if err != nil {
		return "", false, fmt.Errorf("resolve Kimia home %q: %w", p.home, err)
	}
	if relative, ok := relativeWithin(homeReal, contextReal); ok {
		p.Context = filepath.Join(p.home, relative)
		return contextReal, true, nil
	}

	workspaceRoots, err := configuredWorkspaceRoots(p.workingDir)
	if err != nil {
		return "", false, err
	}
	allowed := false
	for _, root := range workspaceRoots {
		if _, ok := relativeWithin(root, contextReal); ok {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", false, fmt.Errorf(
			"local build context %q is outside HOME and the configured Harness/Drone workspace",
			contextPath,
		)
	}

	privateRoot, err := p.ensurePrivateRoot()
	if err != nil {
		return "", false, err
	}
	proxy := filepath.Join(privateRoot, "context")
	if err := os.Symlink(contextReal, proxy); err != nil {
		return "", false, fmt.Errorf("create private build-context proxy: %w", err)
	}
	p.Context = proxy
	return contextReal, true, nil
}

func (p *Plan) prepareTarPath(value string) error {
	if value == "" {
		return nil
	}
	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("tar path contains a null byte")
	}

	target, err := absoluteFrom(p.workingDir, value)
	if err != nil {
		return fmt.Errorf("resolve tar path %q: %w", value, err)
	}
	if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
		return fmt.Errorf("tar path %q is a directory", target)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect tar path %q: %w", target, statErr)
	}

	insideHome, err := potentialPathWithin(p.home, target)
	if err != nil {
		return fmt.Errorf("validate tar path %q: %w", target, err)
	}
	if filepath.IsAbs(value) && insideHome {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create tar output directory %q: %w", filepath.Dir(target), err)
		}
		p.TarPath = target
		return nil
	}

	privateRoot, err := p.ensurePrivateRoot()
	if err != nil {
		return err
	}
	outputDir := filepath.Join(privateRoot, "output")
	if err := os.Mkdir(outputDir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create private tar output directory: %w", err)
	}
	p.TarPath = filepath.Join(outputDir, "image.tar")
	p.tarDestination = target
	return nil
}

func (p *Plan) ensurePrivateRoot() (string, error) {
	if p.privateRoot != "" {
		return p.privateRoot, nil
	}
	root, err := os.MkdirTemp(p.home, ".drone-kimia-")
	if err != nil {
		return "", fmt.Errorf("create private workspace compatibility directory: %w", err)
	}
	p.privateRoot = root
	return root, nil
}

func configuredWorkspaceRoots(workingDir string) ([]string, error) {
	var roots []string
	var invalid []error
	configured := false
	for _, key := range []string{"HARNESS_WORKSPACE", "DRONE_WORKSPACE"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		configured = true
		path, err := absoluteFrom(workingDir, value)
		if err != nil {
			invalid = append(invalid, fmt.Errorf("resolve %s: %w", key, err))
			continue
		}
		if err := ensureDirectory(path); err != nil {
			invalid = append(invalid, fmt.Errorf("validate %s %q: %w", key, path, err))
			continue
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			invalid = append(invalid, fmt.Errorf("resolve %s %q: %w", key, path, err))
			continue
		}
		roots = append(roots, filepath.Clean(path))
	}
	if len(roots) != 0 {
		return roots, nil
	}
	if configured {
		return nil, fmt.Errorf("no configured Harness/Drone workspace is usable: %w", errors.Join(invalid...))
	}
	path, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve startup working directory %q: %w", workingDir, err)
	}
	roots = append(roots, filepath.Clean(path))
	return roots, nil
}

func relativeDockerfile(contextPath, dockerfile string) (string, error) {
	dockerfilePath := filepath.Clean(dockerfile)
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("inspect Dockerfile %q: %w", dockerfilePath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Dockerfile %q is not a regular file", dockerfilePath)
	}
	dockerfileReal, err := filepath.EvalSymlinks(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("resolve Dockerfile %q: %w", dockerfilePath, err)
	}
	relative, ok := relativeWithin(contextPath, dockerfileReal)
	if !ok || relative == "." {
		return "", fmt.Errorf("absolute Dockerfile %q is outside build context %q", dockerfilePath, contextPath)
	}
	return relative, nil
}

func absoluteFrom(base, path string) (string, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("path contains a null byte")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(filepath.Clean(path))
}

func relativeWithin(base, path string) (string, bool) {
	relative, err := filepath.Rel(filepath.Clean(base), filepath.Clean(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func potentialPathWithin(base, path string) (bool, error) {
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false, err
	}
	pathReal, err := resolvePotentialPath(path)
	if err != nil {
		return false, err
	}
	_, ok := relativeWithin(baseReal, pathReal)
	return ok, nil
}

// resolvePotentialPath evaluates symlinks in the longest existing prefix and
// appends any components that have not been created yet.
func resolvePotentialPath(path string) (string, error) {
	path = filepath.Clean(path)
	probe := path
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func ensureDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

func isRemoteContext(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"http://", "https://", "git://", "ssh://", "git@"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func atomicCopy(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open staged image tar: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect staged image tar: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged image tar is not a regular file")
	}

	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".drone-kimia-tar-")
	if err != nil {
		return fmt.Errorf("create temporary destination: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := io.Copy(temporary, input); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("copy staged image tar: %w", err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o600
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set image tar permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync image tar: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close image tar: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	return nil
}
