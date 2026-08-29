package tags

import (
	"fmt"
	"strings"

	"github.com/coreos/go-semver/semver"
)

func Resolve(input []string, expand, automatic bool, suffix, ref, defaultBranch string) ([]string, error) {
	if expand && automatic {
		return nil, fmt.Errorf("PLUGIN_EXPAND_TAG conflicts with PLUGIN_AUTO_TAG/PLUGIN_DEFAULT_TAGS")
	}
	if automatic {
		if len(input) > 1 || (len(input) == 1 && input[0] != "latest") {
			return nil, fmt.Errorf("automatic tags cannot be combined with explicit tags %v", input)
		}
		return auto(ref, defaultBranch, suffix)
	}
	if !expand {
		return deduplicate(input), nil
	}

	var result []string
	for _, tag := range input {
		expanded, err := expandOne(tag)
		if err != nil {
			result = append(result, tag)
			continue
		}
		result = append(result, expanded...)
	}
	return deduplicate(result), nil
}

func auto(ref, defaultBranch, suffix string) ([]string, error) {
	if strings.HasPrefix(ref, "refs/tags/") {
		value := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/tags/"), "v")
		version, err := semver.NewVersion(value)
		if err != nil {
			return nil, fmt.Errorf("auto-tag reference %q is not semantic version: %w", ref, err)
		}
		var result []string
		if version.PreRelease != "" || version.Metadata != "" {
			result = []string{version.String()}
		} else if version.Major == 0 {
			result = []string{
				fmt.Sprintf("%d.%d", version.Major, version.Minor),
				version.String(),
			}
		} else {
			result = []string{
				fmt.Sprintf("%d", version.Major),
				fmt.Sprintf("%d.%d", version.Major, version.Minor),
				version.String(),
			}
		}
		return addSuffix(result, suffix), nil
	}

	branch := strings.TrimPrefix(ref, "refs/heads/")
	if branch == defaultBranch && branch != "" {
		return addSuffix([]string{"latest"}, suffix), nil
	}
	return nil, fmt.Errorf("cannot auto-tag ref %q on default branch %q", ref, defaultBranch)
}

func expandOne(tag string) ([]string, error) {
	value := strings.ReplaceAll(strings.TrimPrefix(tag, "v"), "_", "-")
	value = normalizePartial(value)
	version, err := semver.NewVersion(value)
	if err != nil {
		return nil, err
	}
	if version.PreRelease != "" {
		return []string{version.String()}, nil
	}
	metadata := ""
	if version.Metadata != "" {
		metadata = "+" + version.Metadata
	}
	return []string{
		fmt.Sprintf("%d%s", version.Major, metadata),
		fmt.Sprintf("%d.%d%s", version.Major, version.Minor, metadata),
		version.String(),
	}, nil
}

func normalizePartial(value string) string {
	core := value
	suffix := ""
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		core, suffix = value[:index], value[index:]
	}
	switch strings.Count(core, ".") {
	case 0:
		core += ".0.0"
	case 1:
		core += ".0"
	}
	return core + suffix
}

func addSuffix(input []string, suffix string) []string {
	if suffix == "" {
		return input
	}
	result := make([]string, len(input))
	for index, tag := range input {
		if tag == "latest" {
			result[index] = suffix
		} else {
			result[index] = tag + "-" + suffix
		}
	}
	return result
}

func deduplicate(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, value := range input {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
