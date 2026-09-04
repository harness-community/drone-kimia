package main

import (
	"context"
	"fmt"
	"os"

	"github.com/harness-community/drone-kimia/internal/releaseverify"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "release verification failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	return releaseverify.Verify(ctx, releaseverify.Options{
		RepositoryPrefix: releaseverify.DefaultRepositoryPrefix,
		ReleaseTag:       os.Getenv("KIMIA_RELEASE_TAG"),
		Revision:         os.Getenv("KIMIA_RELEASE_REVISION"),
		Username:         os.Getenv("DOCKER_USERNAME"),
		Password:         os.Getenv("DOCKER_PASSWORD"),
		Writer:           os.Stdout,
	})
}
