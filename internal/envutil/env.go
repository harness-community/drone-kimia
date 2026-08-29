package envutil

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// LoadFile loads PLUGIN_ENV_FILE before the rest of the plugin inputs are read.
func LoadFile() error {
	path := strings.TrimSpace(os.Getenv("PLUGIN_ENV_FILE"))
	if path == "" {
		return nil
	}
	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("load PLUGIN_ENV_FILE %q: %w", path, err)
	}
	return nil
}

func First(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func Bool(keys ...string) (bool, error) {
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return false, fmt.Errorf("%s must be a boolean: %w", key, err)
		}
		return parsed, nil
	}
	return false, nil
}

func Int(keys ...string) (int, error) {
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return parsed, nil
	}
	return 0, nil
}

func CSV(value string) []string {
	return split(value, ",")
}

func Semicolon(value string) []string {
	return split(value, ";")
}

func LinesOrCSV(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\n", ",")
	return CSV(value)
}

func split(value, separator string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
