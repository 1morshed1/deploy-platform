// Command seed is a development utility that creates a tenant and prints a fresh
// API key. The raw key is shown ONCE here (we only store its hash), so copy it.
//
// Usage:
//
//	go run ./cmd/seed -email you@example.com
//
// It reads DATABASE_URL from the environment like the other binaries.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/1morshed1/deploy-platform/internal/api"
	"github.com/1morshed1/deploy-platform/internal/config"
	"github.com/1morshed1/deploy-platform/internal/store"
)

func main() {
	email := flag.String("email", "dev@example.com", "tenant email")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fail(err)
	}
	defer st.Close()

	raw, hash, err := api.GenerateAPIKey()
	if err != nil {
		fail(err)
	}
	t, err := st.CreateTenant(ctx, *email, hash)
	if err != nil {
		fail(err)
	}

	fmt.Printf("Tenant created:\n  id:    %s\n  email: %s\n\n", t.ID, t.Email)
	fmt.Printf("API key (store it now, it will not be shown again):\n  %s\n", raw)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "seed error:", err)
	os.Exit(1)
}
