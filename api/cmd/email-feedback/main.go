// Command email-feedback is the SNS-triggered Lambda that consumes SES bounce
// and complaint notifications. See EMAIL.md §6.
//
// Behavior:
//   - Hard bounce → User.emailStatus = "bounced"; subsequent backend sends are
//     suppressed at the SDK boundary.
//   - Complaint → User.emailStatus = "complained"; suppress + alarm.
//   - Soft bounce → log only in v1 (counter tuning is an open item).
//   - Delivery → no-op other than an EmailEvent forensic row.
//
// Every notification, regardless of outcome, writes an EmailEvent of the
// matching kind so the audit-log preserves the wire trail.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func main() {
	logger := numunlog.NewJSON(os.Stdout, nil)
	slog.SetDefault(logger)

	if observability.InitFromEnv("email-feedback", logger) {
		defer observability.Flush()
	}

	ctx := context.Background()
	st, err := store.New(ctx)
	if err != nil {
		logger.Error("init store", "err", err)
		os.Exit(1)
	}
	h := &handler{store: st, logger: logger}
	lambda.Start(h.Handle)
}

type handler struct {
	store  *store.Client
	logger *slog.Logger
}

// SES → SNS payload shapes per
// https://docs.aws.amazon.com/ses/latest/dg/notification-contents.html.
// We unmarshal only the fields we act on.

type sesNotification struct {
	NotificationType string        `json:"notificationType"`
	Mail             sesMail       `json:"mail"`
	Bounce           *sesBounce    `json:"bounce,omitempty"`
	Complaint        *sesComplaint `json:"complaint,omitempty"`
	Delivery         *sesDelivery  `json:"delivery,omitempty"`
}

type sesMail struct {
	MessageID string `json:"messageId"`
	Source    string `json:"source"`
}

type sesBouncedRecipient struct {
	EmailAddress string `json:"emailAddress"`
}

type sesBounce struct {
	BounceType        string                `json:"bounceType"` // "Permanent" | "Transient" | "Undetermined"
	BounceSubType     string                `json:"bounceSubType"`
	BouncedRecipients []sesBouncedRecipient `json:"bouncedRecipients"`
}

type sesComplaint struct {
	ComplainedRecipients  []sesBouncedRecipient `json:"complainedRecipients"`
	ComplaintFeedbackType string                `json:"complaintFeedbackType,omitempty"`
}

type sesDelivery struct {
	Recipients []string `json:"recipients"`
}

func (h *handler) Handle(ctx context.Context, evt events.SNSEvent) error {
	for _, rec := range evt.Records {
		if err := h.processOne(ctx, rec.SNS.Message); err != nil {
			// Don't stop the loop on individual errors — SNS doesn't redrive
			// per-record, so we log and continue rather than swallowing all.
			h.logger.Error("feedback: process record", "err", err)
		}
	}
	return nil
}

func (h *handler) processOne(ctx context.Context, rawMessage string) error {
	var n sesNotification
	if err := json.Unmarshal([]byte(rawMessage), &n); err != nil {
		return fmt.Errorf("unmarshal SES notification: %w", err)
	}
	switch n.NotificationType {
	case "Bounce":
		return h.handleBounce(ctx, n)
	case "Complaint":
		return h.handleComplaint(ctx, n)
	case "Delivery":
		return h.handleDelivery(ctx, n)
	default:
		h.logger.Warn("feedback: unknown notification type", "type", n.NotificationType)
		return nil
	}
}

func (h *handler) handleBounce(ctx context.Context, n sesNotification) error {
	if n.Bounce == nil {
		return errors.New("bounce payload missing")
	}
	for _, r := range n.Bounce.BouncedRecipients {
		ev := domain.EmailEvent{
			RecipientEmail: r.EmailAddress,
			Kind:           domain.EmailKindBounceReceived,
			Status:         domain.EmailEventStatusBounce,
			SESMessageID:   n.Mail.MessageID,
			FailureReason:  fmt.Sprintf("%s/%s", n.Bounce.BounceType, n.Bounce.BounceSubType),
		}
		// Hard bounces suppress; soft bounces just log in v1.
		if strings.EqualFold(n.Bounce.BounceType, "Permanent") {
			user, err := h.store.FindUserByEmail(ctx, r.EmailAddress)
			if err == nil {
				patch := store.UpdateUserPatch{}
				bounced := domain.EmailStatusBounced
				patch.EmailStatus = &bounced
				if _, err := h.store.UpdateUser(ctx, user.ID, user.Version, patch); err != nil {
					h.logger.Warn("feedback: suppress (bounce) failed", "userId", user.ID, "err", err)
				}
				ev.UserID = user.ID
			}
		}
		_ = h.store.RecordEmailEvent(ctx, ev)
	}
	return nil
}

func (h *handler) handleComplaint(ctx context.Context, n sesNotification) error {
	if n.Complaint == nil {
		return errors.New("complaint payload missing")
	}
	for _, r := range n.Complaint.ComplainedRecipients {
		ev := domain.EmailEvent{
			RecipientEmail: r.EmailAddress,
			Kind:           domain.EmailKindComplaintReceived,
			Status:         domain.EmailEventStatusComplaint,
			SESMessageID:   n.Mail.MessageID,
			FailureReason:  n.Complaint.ComplaintFeedbackType,
		}
		user, err := h.store.FindUserByEmail(ctx, r.EmailAddress)
		if err == nil {
			complained := domain.EmailStatusComplained
			patch := store.UpdateUserPatch{EmailStatus: &complained}
			if _, err := h.store.UpdateUser(ctx, user.ID, user.Version, patch); err != nil {
				h.logger.Warn("feedback: suppress (complaint) failed", "userId", user.ID, "err", err)
			}
			ev.UserID = user.ID
		}
		_ = h.store.RecordEmailEvent(ctx, ev)
	}
	return nil
}

func (h *handler) handleDelivery(ctx context.Context, n sesNotification) error {
	if n.Delivery == nil {
		return nil
	}
	for _, addr := range n.Delivery.Recipients {
		ev := domain.EmailEvent{
			RecipientEmail: addr,
			Kind:           domain.EmailKindDeliveryConfirmed,
			Status:         domain.EmailEventStatusDelivery,
			SESMessageID:   n.Mail.MessageID,
		}
		if user, err := h.store.FindUserByEmail(ctx, addr); err == nil {
			ev.UserID = user.ID
		}
		_ = h.store.RecordEmailEvent(ctx, ev)
	}
	return nil
}
