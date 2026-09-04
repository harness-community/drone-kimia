package plugincli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/harness-community/drone-kimia/internal/app"
	"github.com/harness-community/drone-kimia/internal/envutil"
	"github.com/harness-community/drone-kimia/internal/version"
	"github.com/urfave/cli"
)

const kimiaVersion = "1.0.26"

// Options identifies the provider wrapper and the provider-owned flags that
// are declared in its cmd/kimia-*/main.go entrypoint.
type Options struct {
	Provider      string
	ProviderFlags []cli.Flag
}

// Main runs a provider wrapper using the process arguments and streams.
func Main(options Options) int {
	return Execute(options, os.Args, app.Streams{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

// Execute is Main's testable implementation.
func Execute(options Options, arguments []string, streams app.Streams) int {
	if err := setEnvFileFromArguments(arguments); err != nil {
		printError(streams.Stderr, err)
		return 2
	}
	// The reference Drone plugins load PLUGIN_ENV_FILE before urfave/cli reads
	// EnvVar values. Keep that ordering for every provider.
	if err := envutil.LoadFile(); err != nil {
		printError(streams.Stderr, err)
		return 1
	}

	application, flags, err := newApplication(options, streams)
	if err != nil {
		printError(streams.Stderr, err)
		return 1
	}
	application.Action = func(context *cli.Context) error {
		if context.NArg() != 0 {
			return cli.NewExitError("positional arguments are not supported; use the named plugin flags", 2)
		}
		if err := ApplyContext(context, flags); err != nil {
			return err
		}
		return app.RunWithSignals(options.Provider, streams)
	}

	if err := application.Run(arguments); err != nil {
		printError(streams.Stderr, err)
		if exitCoder, ok := err.(cli.ExitCoder); ok && exitCoder.ExitCode() > 0 {
			return exitCoder.ExitCode()
		}
		return app.ExitCode(err)
	}
	return 0
}

func newApplication(options Options, streams app.Streams) (*cli.App, []cli.Flag, error) {
	provider := strings.ToLower(strings.TrimSpace(options.Provider))
	switch provider {
	case "docker", "gar", "ecr", "acr":
	default:
		return nil, nil, fmt.Errorf("unsupported provider %q", options.Provider)
	}

	flags := append(CommonFlags(), options.ProviderFlags...)
	application := cli.NewApp()
	application.Name = "drone-kimia"
	application.Usage = fmt.Sprintf("Buildah-only Harness/Drone image builder for %s", strings.ToUpper(provider))
	application.Description = "Maps existing Drone Docker/Kaniko inputs and native Kimia inputs to RapidFort Kimia."
	application.Version = fmt.Sprintf(
		"%s (%s, %s); provider=%s; kimia=%s",
		version.Version,
		version.Commit,
		version.BuildDate,
		provider,
		kimiaVersion,
	)
	application.Flags = flags
	application.Writer = writerOrDiscard(streams.Stdout)
	application.ErrWriter = writerOrDiscard(streams.Stderr)
	return application, flags, nil
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

func printError(writer io.Writer, err error) {
	if writer == nil || err == nil {
		return
	}
	fmt.Fprintln(writer, "drone-kimia:", err)
}

// ApplyContext exports explicitly resolved CLI values to their canonical
// environment names. The existing config and auth packages then consume one
// input path for both Harness-provided PLUGIN_* values and direct CLI flags.
func ApplyContext(context *cli.Context, flags []cli.Flag) error {
	for _, flag := range flags {
		name := primaryName(flag.GetName())
		if name == "" || !context.IsSet(name) {
			continue
		}
		environment, value, ok, err := resolvedValue(context, flag, name)
		if err != nil {
			return err
		}
		if !ok || environment == "" {
			continue
		}
		if err := os.Setenv(environment, value); err != nil {
			return fmt.Errorf("set %s from --%s: %w", environment, name, err)
		}
	}
	return nil
}

func resolvedValue(context *cli.Context, flag cli.Flag, name string) (string, string, bool, error) {
	switch typed := flag.(type) {
	case cli.StringFlag:
		return firstEnvironment(typed.EnvVar), context.String(name), true, nil
	case cli.BoolFlag:
		return firstEnvironment(typed.EnvVar), strconv.FormatBool(context.Bool(name)), true, nil
	case cli.BoolTFlag:
		return firstEnvironment(typed.EnvVar), strconv.FormatBool(context.BoolT(name)), true, nil
	case cli.IntFlag:
		return firstEnvironment(typed.EnvVar), strconv.Itoa(context.Int(name)), true, nil
	case cli.StringSliceFlag:
		return firstEnvironment(typed.EnvVar), strings.Join(context.StringSlice(name), ","), true, nil
	case cli.GenericFlag:
		value := context.Generic(name)
		if value == nil {
			return firstEnvironment(typed.EnvVar), "", true, nil
		}
		if list, ok := value.(*semicolonSlice); ok {
			return firstEnvironment(typed.EnvVar), list.joined(), true, nil
		}
		stringValue, ok := value.(fmt.Stringer)
		if !ok {
			return "", "", false, fmt.Errorf("generic CLI flag --%s does not provide a string value", name)
		}
		return firstEnvironment(typed.EnvVar), stringValue.String(), true, nil
	default:
		return "", "", false, fmt.Errorf("unsupported CLI flag type %T for --%s", flag, name)
	}
}

func primaryName(value string) string {
	name, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(name)
}

func firstEnvironment(value string) string {
	name, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(name)
}

func setEnvFileFromArguments(arguments []string) error {
	for index := 1; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--env-file":
			if index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" {
				return fmt.Errorf("--env-file requires a path")
			}
			return os.Setenv("PLUGIN_ENV_FILE", arguments[index+1])
		case strings.HasPrefix(argument, "--env-file="):
			path := strings.TrimSpace(strings.TrimPrefix(argument, "--env-file="))
			if path == "" {
				return fmt.Errorf("--env-file requires a path")
			}
			return os.Setenv("PLUGIN_ENV_FILE", path)
		}
	}
	return nil
}
