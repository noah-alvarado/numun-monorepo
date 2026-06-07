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
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

var logger *slog.Logger

func main() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx := context.Background()
	st, err := store.New(ctx)
	if err != nil {
		logger.Error("init store", "err", err)
		os.Exit(1)
	}
	h := &handler{store: st}
	lambda.Start(h.Handle)
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
