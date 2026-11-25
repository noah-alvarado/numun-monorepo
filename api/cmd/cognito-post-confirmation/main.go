// Command cognito-post-confirmation is the Cognito post-confirmation trigger
// Lambda. See AUTH.md §3.1.
//
// On successful ConfirmSignUp, this trigger writes the application-side User
// row + a signup_completed AuthAuditEvent so the very first authenticated API
// call doesn't race against a missing User mirror.
//
// Eligible trigger sources: PostConfirmation_ConfirmSignUp,
// PostConfirmation_ConfirmForgotPassword. We only write the User row on the
// signup event; password resets do not warrant a new mirror.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/numun/numun/api/internal/domain"
	numunlog "github.com/numun/numun/api/internal/log"
	"github.com/numun/numun/api/internal/observability"
	"github.com/numun/numun/api/internal/store"
)

var logger *slog.Logger

func main() {
	logger = numunlog.NewJSON(os.Stdout, nil)
	slog.SetDefault(logger)

	if observability.InitFromEnv("cognito-post-confirmation", logger) {
		defer observability.Flush()
	}

	ctx := context.Background()
	st, err := store.New(ctx)
	if err != nil {
		logger.Error("init store", "err", err)
		os.Exit(1)
	}
	h := &handler{store: st}
	if len(os.Args) > 1 && os.Args[1] == "-local" {
		os.Exit(runLocal(ctx, h))
	}
	lambda.Start(h.Handle)
}

// runLocal handles one trigger event read from stdin, so the e2e fake
// Cognito can invoke the trigger the way Cognito does. Only allowed with
// DEV_MODE=true.
func runLocal(ctx context.Context, h *handler) int {
	if os.Getenv("DEV_MODE") != "true" {
		logger.Error("-local requires DEV_MODE=true")
		return 2
	}
	var e events.CognitoEventUserPoolsPostConfirmation
	if err := json.NewDecoder(os.Stdin).Decode(&e); err != nil {
		logger.Error("decode event", "err", err)
		return 1
	}
	if _, err := h.Handle(ctx, e); err != nil {
		logger.Error("handle", "err", err)
		return 1
	}
	return 0
}

type handler struct {
	store *store.Client
}

// Handle returns the event unchanged — Cognito requires the response to echo
// the inbound event. If the side-effect write fails we still let the user in,
// but we log loudly: the lazy-create path in AuthService.Exchange will catch
// the miss on the first authenticated call.
func (h *handler) Handle(ctx context.Context, e events.CognitoEventUserPoolsPostConfirmation) (events.CognitoEventUserPoolsPostConfirmation, error) {
	if e.TriggerSource != "PostConfirmation_ConfirmSignUp" {
		// Pass through unchanged for forgot-password and other triggers.
		return e, nil
	}

	sub := e.Request.UserAttributes["sub"]
	if sub == "" {
		logger.Error("post-confirmation: missing sub attribute", "userPoolId", e.UserPoolID)
		return e, nil
	}

	role := domain.Role(e.Request.UserAttributes["custom:role"])
	if role != domain.RoleStaffAdmin && role != domain.RoleStaffStaffer {
		role = domain.RoleAdvisor
	}

	user := domain.User{
		ID:    sub,
		Role:  role,
		Email: strings.ToLower(e.Request.UserAttributes["email"]),
		Name:  e.Request.UserAttributes["name"],
		Phone: e.Request.UserAttributes["phone_number"],
		// DATA_MODEL.md: announcements are opt-out.
		AnnouncementsOptIn: true,
	}

	if _, err := h.store.CreateUser(ctx, user); err != nil && !errors.Is(err, store.ErrAlreadyExists) {
		logger.Error("post-confirmation: create user", "err", err, "sub", sub)
		// Don't fail the trigger — Cognito would block the sign-up and the
		// user would be stuck unconfirmed.
		return e, nil
	}

	if err := h.store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      sub,
		ActorUserID: sub,
		Kind:        domain.AuthEventSignupCompleted,
		Metadata:    map[string]string{"role": string(role)},
	}); err != nil {
		logger.Warn("post-confirmation: audit", "err", err, "sub", sub)
	}

	return e, nil
}
