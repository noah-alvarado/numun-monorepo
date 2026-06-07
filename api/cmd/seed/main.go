// Command seed populates DynamoDB Local with the dev-loop seed dataset.
//
// Reads connection info from the same env vars as cmd/api
// (AWS_ENDPOINT_URL_DYNAMODB, AWS_REGION, etc.) so it Just Works under
// `make dev`. Refuses to run against a real AWS endpoint as a guardrail.
//
// See /docs/seed-users.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// SeedUser is the seed-user catalog. Keep these IDs stable — the portal's
// "Sign in as…" debug shortcut and the X-Dev-User-Id docs reference them
// verbatim. Generated as UUIDv7s once and pinned.
type SeedUser struct {
	ID    string
	Role  domain.Role
	Email string
	Name  string
	Phone string
}

var Users = []SeedUser{
	{
		ID:    "0190a000-0000-7000-8000-000000000001",
		Role:  domain.RoleAdvisor,
		Email: "advisor@seed.numun.local",
		Name:  "Seed Advisor",
		Phone: "+15555550101",
	},
	{
		ID:    "0190a000-0000-7000-8000-000000000002",
		Role:  domain.RoleStaffStaffer,
		Email: "staffer@seed.numun.local",
		Name:  "Seed Staffer",
		Phone: "+15555550102",
	},
	{
		ID:    "0190a000-0000-7000-8000-000000000003",
		Role:  domain.RoleStaffAdmin,
		Email: "admin@seed.numun.local",
		Name:  "Seed Admin",
		Phone: "+15555550103",
	},
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Guardrail: this is a local-dev tool. Refuse to run unless one of the
	// two recognized local endpoints is set.
	endpoint := os.Getenv("AWS_ENDPOINT_URL_DYNAMODB")
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "seed: refusing to run without AWS_ENDPOINT_URL_DYNAMODB (this command is local-only)")
		os.Exit(2)
	}

	ctx := context.Background()
	c, err := store.New(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed: init store:", err)
		os.Exit(1)
	}

	for _, u := range Users {
		_, err := c.CreateUser(ctx, domain.User{
			ID:    u.ID,
			Role:  u.Role,
			Email: u.Email,
			Name:  u.Name,
			Phone: u.Phone,
		})
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			logger.Info("seed: user exists", "id", u.ID, "role", u.Role)
		case err != nil:
			logger.Error("seed: create user", "id", u.ID, "err", err)
			os.Exit(1)
		default:
			logger.Info("seed: created user", "id", u.ID, "role", u.Role, "email", u.Email)
		}
	}

	fmt.Println()
	fmt.Println("Seed complete. Use these with `X-Dev-User-Id` when DEV_BYPASS_AUTH=true:")
	for _, u := range Users {
		fmt.Printf("  %-14s %s  (%s)\n", u.Role, u.ID, u.Email)
	}
}
