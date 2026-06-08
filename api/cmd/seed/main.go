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
	"time"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// SeedConferenceID is pinned so portal flows can reference it deterministically
// during local dev.
const SeedConferenceID = "0190a000-0000-7000-9000-000000000001"

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

	// Seed one open-for-registration Conference so the portal's M4 flows have
	// a target. Status flips can be driven through the admin UI later.
	now := time.Now().UTC()
	conf := domain.Conference{
		ID:            SeedConferenceID,
		Name:          "NUMUN XXIV (Seed)",
		EditionNumber: 24,
		Year:          now.Year(),
		StartsAt:      now.AddDate(0, 1, 0),
		EndsAt:        now.AddDate(0, 1, 3),
		Status:        domain.ConferenceStatusOpenForRegistration,
		Metadata: map[string]string{
			"theme":    "Seed dataset",
			"location": "Local development",
		},
	}
	if _, err := c.CreateConference(ctx, conf); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			logger.Info("seed: conference exists", "id", conf.ID)
		} else {
			logger.Error("seed: create conference", "err", err)
			os.Exit(1)
		}
	} else {
		logger.Info("seed: created conference", "id", conf.ID, "name", conf.Name)
	}

	fmt.Println()
	fmt.Println("Seed complete. Use these with `X-Dev-User-Id` when DEV_BYPASS_AUTH=true:")
	for _, u := range Users {
		fmt.Printf("  %-14s %s  (%s)\n", u.Role, u.ID, u.Email)
	}
	fmt.Println()
	fmt.Printf("Active conference id: %s\n", SeedConferenceID)
}
