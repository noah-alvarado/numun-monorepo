// Package email is the EMAIL.md subsystem implementation: template rendering,
// SES send, SQS enqueue, and the shared `email.Send` helper used by both the
// synchronous transactional path (handlers) and the async worker Lambda.
//
// The package intentionally exposes a narrow Service interface so handlers can
// be wired against a fake during unit tests; the production wiring constructs
// a Client backed by SES + SQS.
package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// SendRequest is the input to the shared `Send` helper.
//
// `User` is the recipient (we look up `EmailStatus` to honor suppression and
// `AnnouncementsOptIn` for kind=announcement). `Kind` selects the template;
// `Vars` are the per-kind substitutions per EMAIL.md §3.4. `ClientToken` lets
// the worker skip redeliveries — leave empty for transactional sync sends.
type SendRequest struct {
	User          domain.User
	Kind          domain.EmailKind
	Subject       string
	Vars          map[string]any
	ClientToken   string
	SenderAddress string // overrides default (`noreply@…`)
}

// EnqueueRequest is the SQS payload shape. Identical to SendRequest minus the
// dereferenced User — only the userId crosses the wire; the worker re-resolves
// the User so suppression is rechecked at delivery time.
type EnqueueRequest struct {
	UserID         string           `json:"userId,omitempty"`
	RecipientEmail string           `json:"recipientEmail,omitempty"`
	Kind           domain.EmailKind `json:"kind"`
	Subject        string           `json:"subject,omitempty"`
	Vars           map[string]any   `json:"vars,omitempty"`
	ClientToken    string           `json:"clientToken"`
	SenderAddress  string           `json:"senderAddress,omitempty"`

	// New-registration debounce uses these instead of UserID at enqueue time;
	// the worker expands them into per-admin sends.
	ConferenceID    string    `json:"conferenceId,omitempty"`
	WindowStartedAt time.Time `json:"windowStartedAt,omitempty"`
	AnnouncementID  string    `json:"announcementId,omitempty"`
}

// Service is the narrow surface handlers see. A fake implements this in tests.
type Service interface {
	Send(ctx context.Context, req SendRequest) error
	Enqueue(ctx context.Context, req EnqueueRequest, delay time.Duration) error
}

// Config carries runtime-resolved settings sourced from environment variables
// in production. Wired in SAM (infra/api/template.yaml).
type Config struct {
	SenderTransactional string
	SenderAnnouncements string
	SenderCognito       string
	ReplyTo             string // empty → omit Reply-To
	UnsubscribeBaseURL  string
	UnsubscribeSecret   string // HMAC key for stateless unsubscribe tokens
	PortalBaseURL       string
	AssetsBaseURL       string
	BrandColor          string
	ConfigurationSet    string // SES configuration set name (optional)
	QueueURL            string // SQS queue URL for the worker pipeline
}

// LoadConfigFromEnv resolves Config. Env-specific URLs (senders, portal,
// assets, unsubscribe) are required — we refuse to bake apex defaults so a
// misconfigured deploy doesn't silently send mail with prod URLs from a
// test stack. SAM (infra/api/template.yaml) sets all required values;
// make dev wires them through scripts/sam-env-vars.json.
func LoadConfigFromEnv() Config {
	return Config{
		SenderTransactional: os.Getenv("EMAIL_SENDER_TRANSACTIONAL"),
		SenderAnnouncements: os.Getenv("EMAIL_SENDER_ANNOUNCEMENTS"),
		SenderCognito:       os.Getenv("EMAIL_SENDER_COGNITO"),
		ReplyTo:             os.Getenv("EMAIL_REPLY_TO"),
		UnsubscribeBaseURL:  os.Getenv("EMAIL_UNSUBSCRIBE_BASE_URL"),
		UnsubscribeSecret:   os.Getenv("EMAIL_UNSUBSCRIBE_SECRET"),
		PortalBaseURL:       os.Getenv("PORTAL_BASE_URL"),
		AssetsBaseURL:       os.Getenv("ASSETS_BASE_URL"),
		BrandColor:          envOr("EMAIL_BRAND_COLOR", "#4E2A84"),
		ConfigurationSet:    os.Getenv("EMAIL_CONFIGURATION_SET"),
		QueueURL:            os.Getenv("EMAIL_QUEUE_URL"),
	}
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// Validate refuses to start with empty env-specific URLs. Called from New()
// outside DEV_MODE so prod / test deploys fail fast on a misconfigured SAM
// stack rather than silently baking the wrong apex into outbound mail.
func (c Config) Validate() error {
	var missing []string
	for _, kv := range []struct {
		name string
		val  string
	}{
		{"EMAIL_SENDER_TRANSACTIONAL", c.SenderTransactional},
		{"EMAIL_SENDER_ANNOUNCEMENTS", c.SenderAnnouncements},
		{"EMAIL_SENDER_COGNITO", c.SenderCognito},
		{"EMAIL_UNSUBSCRIBE_BASE_URL", c.UnsubscribeBaseURL},
		{"PORTAL_BASE_URL", c.PortalBaseURL},
		{"ASSETS_BASE_URL", c.AssetsBaseURL},
	} {
		if kv.val == "" {
			missing = append(missing, kv.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("email: required env vars unset: %v", missing)
	}
	return nil
}

// Client is the production implementation of Service.
type Client struct {
	SES    sesAPI
	SQS    sqsAPI
	Store  *store.Client
	Logger *slog.Logger
	Cfg    Config
	tpl    *Templates
}

type sesAPI interface {
	SendEmail(ctx context.Context, in *sesv2.SendEmailInput, opts ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

type sqsAPI interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// New wires SES + SQS clients honoring LocalStack endpoint env vars. The
// UnsubscribeSecret is resolved from SSM at /numun/${ENV}/email/unsubscribe_secret
// when not provided via env (same pattern as cmsoauth's state_secret).
func New(ctx context.Context, st *store.Client, logger *slog.Logger) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	ses := sesv2.NewFromConfig(cfg, func(o *sesv2.Options) {
		if ep := os.Getenv("AWS_ENDPOINT_URL_SES"); ep != "" {
			o.BaseEndpoint = aws.String(ep)
		}
	})
	sq := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		if ep := os.Getenv("AWS_ENDPOINT_URL_SQS"); ep != "" {
			o.BaseEndpoint = aws.String(ep)
		}
	})
	tpl, err := LoadTemplates()
	if err != nil {
		return nil, fmt.Errorf("load templates: %w", err)
	}
	emailCfg := LoadConfigFromEnv()
	if os.Getenv("DEV_MODE") != "true" {
		if err := emailCfg.Validate(); err != nil {
			return nil, err
		}
	}
	if emailCfg.UnsubscribeSecret == "" && os.Getenv("DEV_MODE") != "true" {
		// Attempt SSM read; missing-parameter is non-fatal — we'll just skip
		// the List-Unsubscribe header.
		if secret, err := readSSMUnsubscribeSecret(ctx, cfg, logger); err == nil {
			emailCfg.UnsubscribeSecret = secret
		}
	}
	return &Client{
		SES:    ses,
		SQS:    sq,
		Store:  st,
		Logger: logger,
		Cfg:    emailCfg,
		tpl:    tpl,
	}, nil
}

// Templates returns the loaded template set. Useful for tests.
func (c *Client) Templates() *Templates { return c.tpl }

// Send renders and dispatches one message synchronously. Failures write a
// `failed` EmailEvent and return the error; handlers treat the error as
// best-effort (the DDB write is the source of truth; the email is a
// notification — EMAIL.md §5.1).
func (c *Client) Send(ctx context.Context, req SendRequest) error {
	if req.User.Email == "" {
		return errors.New("email: recipient has no email")
	}
	if req.User.EmailStatus != "" && req.User.EmailStatus != domain.EmailStatusOK {
		c.writeEvent(ctx, req, "", domain.EmailEventStatusSkipped, "suppressed:"+string(req.User.EmailStatus))
		return nil
	}
	if req.Kind == domain.EmailKindAnnouncement && !req.User.AnnouncementsOptIn {
		c.writeEvent(ctx, req, "", domain.EmailEventStatusSkipped, "opted-out:announcements")
		return nil
	}

	sender := req.SenderAddress
	if sender == "" {
		if req.Kind == domain.EmailKindAnnouncement {
			sender = c.Cfg.SenderAnnouncements
		} else {
			sender = c.Cfg.SenderTransactional
		}
	}

	data := c.buildTemplateData(req)
	rendered, err := c.tpl.Render(string(req.Kind), data)
	if err != nil {
		c.writeEvent(ctx, req, "", domain.EmailEventStatusFailed, "render: "+err.Error())
		return fmt.Errorf("email render: %w", err)
	}

	subject := req.Subject
	if subject == "" {
		subject = rendered.Subject
	}

	msg := &sesv2types.Message{
		Subject: &sesv2types.Content{Data: aws.String(subject), Charset: aws.String("UTF-8")},
		Body: &sesv2types.Body{
			Html: &sesv2types.Content{Data: aws.String(rendered.HTML), Charset: aws.String("UTF-8")},
			Text: &sesv2types.Content{Data: aws.String(rendered.Text), Charset: aws.String("UTF-8")},
		},
	}
	// Announcement-only List-Unsubscribe headers (EMAIL.md §2.2). Transactional
	// mail is not eligible to be opted out of.
	if req.Kind == domain.EmailKindAnnouncement {
		luURL, err := SignedUnsubscribeURL(c.Cfg.UnsubscribeBaseURL, c.Cfg.UnsubscribeSecret, req.User.ID)
		if err == nil {
			msg.Headers = []sesv2types.MessageHeader{
				{Name: aws.String("List-Unsubscribe"), Value: aws.String("<" + luURL + ">")},
				{Name: aws.String("List-Unsubscribe-Post"), Value: aws.String("List-Unsubscribe=One-Click")},
			}
		}
	}

	in := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(sender),
		Destination:      &sesv2types.Destination{ToAddresses: []string{req.User.Email}},
		Content:          &sesv2types.EmailContent{Simple: msg},
	}
	if c.Cfg.ReplyTo != "" {
		in.ReplyToAddresses = []string{c.Cfg.ReplyTo}
	}
	if c.Cfg.ConfigurationSet != "" {
		in.ConfigurationSetName = aws.String(c.Cfg.ConfigurationSet)
	}

	out, err := c.SES.SendEmail(ctx, in)
	if err != nil {
		c.writeEvent(ctx, req, "", domain.EmailEventStatusFailed, err.Error())
		return fmt.Errorf("ses send: %w", err)
	}

	sesID := ""
	if out != nil && out.MessageId != nil {
		sesID = *out.MessageId
	}
	c.writeEvent(ctx, req, sesID, domain.EmailEventStatusSent, "")
	return nil
}

// Enqueue places a single message on the worker queue. The worker re-checks
// suppression at delivery time. When EMAIL_QUEUE_URL is unset (raw dev loop),
// the call is logged and skipped rather than failing the caller.
func (c *Client) Enqueue(ctx context.Context, req EnqueueRequest, delay time.Duration) error {
	if c.Cfg.QueueURL == "" {
		if c.Logger != nil {
			c.Logger.Warn("email enqueue skipped: EMAIL_QUEUE_URL unset", "kind", req.Kind)
		}
		return nil
	}
	if req.ClientToken == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("uuid v7: %w", err)
		}
		req.ClientToken = id.String()
	}
	body, err := MarshalEnqueue(req)
	if err != nil {
		return err
	}
	in := &sqs.SendMessageInput{
		QueueUrl:    aws.String(c.Cfg.QueueURL),
		MessageBody: aws.String(body),
	}
	if delay > 0 {
		secs := int32(delay / time.Second)
		if secs > 900 { // SQS max
			secs = 900
		}
		in.DelaySeconds = secs
	}
	_, err = c.SQS.SendMessage(ctx, in)
	return err
}

func (c *Client) writeEvent(ctx context.Context, req SendRequest, sesID string, status domain.EmailEventStatus, reason string) {
	if c.Store == nil {
		return
	}
	ev := domain.EmailEvent{
		UserID:         req.User.ID,
		RecipientEmail: req.User.Email,
		Kind:           req.Kind,
		Subject:        req.Subject,
		SenderAddress:  req.SenderAddress,
		SESMessageID:   sesID,
		Status:         status,
		FailureReason:  reason,
		ClientToken:    req.ClientToken,
	}
	if err := c.Store.RecordEmailEvent(ctx, ev); err != nil && c.Logger != nil {
		c.Logger.Warn("email event write failed", "err", err, "kind", req.Kind, "status", status)
	}
}

func (c *Client) buildTemplateData(req SendRequest) TemplateData {
	loc, _ := time.LoadLocation("America/Chicago")
	now := time.Now()
	if loc != nil {
		now = now.In(loc)
	}
	luURL := ""
	if req.Kind == domain.EmailKindAnnouncement {
		luURL, _ = SignedUnsubscribeURL(c.Cfg.UnsubscribeBaseURL, c.Cfg.UnsubscribeSecret, req.User.ID)
	}
	return TemplateData{
		RecipientName:  req.User.Name,
		Subject:        req.Subject,
		NowFormatted:   now.Format("Jan 2, 2006 at 3:04 PM") + " CT",
		BrandColor:     c.Cfg.BrandColor,
		AssetsBaseURL:  c.Cfg.AssetsBaseURL,
		PortalBaseURL:  c.Cfg.PortalBaseURL,
		UnsubscribeURL: luURL,
		Kind:           string(req.Kind),
		Vars:           req.Vars,
	}
}
