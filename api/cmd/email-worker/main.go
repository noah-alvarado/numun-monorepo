// Command email-worker is the SQS-triggered Lambda that drives async sends
// (announcements, new-registration debounce). See EMAIL.md §5.3–§5.4.
//
// Each invocation receives up to 10 SQS records. For each: re-resolve the
// recipient User from DDB (so we catch a suppression that landed after the
// message was enqueued), render the template, call SES, write an EmailEvent.
// Per-message failures bubble back to SQS via BatchItemFailures so the
// successful ones don't get redelivered.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	numunlog "github.com/numun/numun/api/internal/log"
	"github.com/numun/numun/api/internal/observability"
	"github.com/numun/numun/api/internal/store"
)

func main() {
	logger := numunlog.NewJSON(os.Stdout, nil)
	slog.SetDefault(logger)

	if observability.InitFromEnv("email-worker", logger) {
		defer observability.Flush()
	}

	ctx := context.Background()
	st, err := store.New(ctx)
	if err != nil {
		logger.Error("init store", "err", err)
		os.Exit(1)
	}
	es, err := email.New(ctx, st, logger)
	if err != nil {
		logger.Error("init email", "err", err)
		os.Exit(1)
	}
	h := &handler{store: st, email: es, logger: logger}
	lambda.Start(h.Handle)
}

type handler struct {
	store  *store.Client
	email  *email.Client
	logger *slog.Logger
}

// Handle processes one SQS batch. We return BatchItemFailures so SQS only
// redrives the messages that erred — partial failure does not nuke a batch.
func (h *handler) Handle(ctx context.Context, evt events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure
	for _, rec := range evt.Records {
		if err := h.processOne(ctx, rec); err != nil {
			h.logger.Error("worker: process message", "messageId", rec.MessageId, "err", err)
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: rec.MessageId})
		}
	}
	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

func (h *handler) processOne(ctx context.Context, rec events.SQSMessage) error {
	req, err := email.UnmarshalEnqueue(rec.Body)
	if err != nil {
		// Malformed payload — surface to DLQ so we can inspect.
		return fmt.Errorf("unmarshal: %w", err)
	}

	// Idempotency: skip if a prior EmailEvent already wrote this token. EMAIL.md §5.6.
	if req.ClientToken != "" {
		exists, err := h.store.FindEmailEventByClientToken(ctx, req.ClientToken)
		if err != nil {
			h.logger.Warn("worker: idempotency lookup failed (proceeding)", "err", err)
		} else if exists {
			h.logger.Info("worker: skipping duplicate", "clientToken", req.ClientToken, "kind", req.Kind)
			return nil
		}
	}

	switch req.Kind {
	case domain.EmailKindNewRegistrationSummary:
		return h.sendNewRegistrationSummary(ctx, req)
	default:
		return h.sendDirect(ctx, req)
	}
}

// sendDirect handles a per-recipient enqueue (announcements + any future
// per-user worker path). EnqueueRequest carries a userId; we re-resolve and
// dispatch.
func (h *handler) sendDirect(ctx context.Context, req email.EnqueueRequest) error {
	if req.UserID == "" {
		return errors.New("worker: empty userId on direct send")
	}
	user, err := h.store.GetUser(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.logger.Warn("worker: recipient missing", "userId", req.UserID)
			return nil // not an error worth redriving
		}
		return fmt.Errorf("resolve user: %w", err)
	}
	send := email.SendRequest{
		User:          user,
		Kind:          req.Kind,
		Subject:       req.Subject,
		Vars:          req.Vars,
		ClientToken:   req.ClientToken,
		SenderAddress: req.SenderAddress,
	}
	return h.email.Send(ctx, send)
}

// sendNewRegistrationSummary expands the EMAIL.md §7.2 summary into per-admin
// sends. The enqueued payload carries the conferenceId + windowStartedAt; we
// query for delegations created in that window, resolve recipients (all
// staff-admin), and dispatch one personalized email per admin.
func (h *handler) sendNewRegistrationSummary(ctx context.Context, req email.EnqueueRequest) error {
	const windowDuration = 15 * time.Minute
	const maxListed = 20

	confID := strings.TrimSpace(req.ConferenceID)
	if confID == "" {
		return errors.New("worker: missing conferenceId for summary")
	}
	// Use the existing per-conference list and filter in-memory; volumes here
	// are tiny (one window's worth of registrations). The list excludes
	// soft-deleted rows.
	all, _, err := h.store.ListDelegationsByConference(ctx, confID, "", 500)
	if err != nil {
		return fmt.Errorf("list delegations: %w", err)
	}
	windowStart := req.WindowStartedAt
	windowEnd := windowStart.Add(windowDuration)
	var inWindow []domain.Delegation
	for _, d := range all {
		if !d.CreatedAt.Before(windowStart) && d.CreatedAt.Before(windowEnd) {
			inWindow = append(inWindow, d)
		}
	}
	if len(inWindow) == 0 {
		h.logger.Info("worker: empty registration window", "conferenceId", confID, "windowStart", windowStart)
		return nil
	}

	// Build the rows passed to the template. Truncated at 20 with an overflow count.
	rows := make([]map[string]any, 0, maxListed)
	for i, d := range inWindow {
		if i >= maxListed {
			break
		}
		// EMAIL.md §3.4: list of {name, school, advisorEmail, createdAt}.
		advisorEmail := ""
		advisors, err := h.store.ListAdvisorsByDelegation(ctx, d.ID)
		if err == nil {
			for _, a := range advisors {
				if a.Role == domain.AdvisorRoleLead {
					if u, err := h.store.GetUser(ctx, a.UserID); err == nil {
						advisorEmail = u.Email
					}
					break
				}
			}
		}
		rows = append(rows, map[string]any{
			"name":         d.School,
			"school":       d.School,
			"advisorEmail": advisorEmail,
			"createdAt":    d.CreatedAt.Format("Jan 2, 2006 at 3:04 PM") + " CT",
		})
	}
	additional := 0
	if len(inWindow) > maxListed {
		additional = len(inWindow) - maxListed
	}

	conferenceName := confID
	if conf, err := h.store.GetConference(ctx, confID); err == nil {
		conferenceName = conf.Name
	}

	admins, err := h.store.ListUsersByRole(ctx, domain.RoleStaffAdmin)
	if err != nil {
		return fmt.Errorf("list admins: %w", err)
	}
	if len(admins) == 0 {
		h.logger.Warn("worker: no staff-admin recipients for new-registration summary")
		return nil
	}
	for _, admin := range admins {
		send := email.SendRequest{
			User:    admin,
			Kind:    domain.EmailKindNewRegistrationSummary,
			Subject: "",
			Vars: map[string]any{
				"conferenceName":  conferenceName,
				"delegations":     rows,
				"additionalCount": additional,
			},
			// Per-admin client token so a SQS redelivery of the parent
			// summary message is dedup'd on the first admin's send.
			ClientToken: req.ClientToken + ":" + admin.ID,
		}
		if err := h.email.Send(ctx, send); err != nil {
			h.logger.Warn("worker: per-admin send failed", "userId", admin.ID, "err", err)
		}
	}
	return nil
}
