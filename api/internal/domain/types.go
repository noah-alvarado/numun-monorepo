// Package domain holds pure value types shared between the repository layer,
// handlers, and middleware. Domain types are protobuf-independent so that the
// store and middleware can be tested without dragging in the generated stubs.
package domain

import "time"

// Role mirrors Cognito's custom:role attribute. Persisted on the User row.
type Role string

const (
	RoleAdvisor      Role = "advisor"
	RoleStaffStaffer Role = "staff-staffer"
	RoleStaffAdmin   Role = "staff-admin"
)

// EmailStatus mirrors DATA_MODEL.md §2.2.
type EmailStatus string

const (
	EmailStatusOK         EmailStatus = "ok"
	EmailStatusBounced    EmailStatus = "bounced"
	EmailStatusComplained EmailStatus = "complained"
)

// User is the application-side profile mirror of a Cognito identity.
type User struct {
	ID                 string
	Role               Role
	Email              string
	Name               string
	Phone              string
	EmailStatus        EmailStatus
	AnnouncementsOptIn bool
	IsDeleted          bool
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CreatedBy          string
	UpdatedBy          string
}

// Session is a server-side opaque session row keyed by the value placed in the
// `numun_session` cookie. See AUTH.md §13.1.
type Session struct {
	ID                         string
	UserID                     string
	RefreshToken               string
	CachedAccessToken          string
	CachedAccessTokenExpiresAt time.Time
	CSRFToken                  string
	IP                         string
	UserAgent                  string
	CreatedAt                  time.Time
	LastUsedAt                 time.Time
	ExpiresAt                  time.Time
}

// AuthAuditEventKind enumerates the values listed in AUTH.md §11.
type AuthAuditEventKind string

const (
	AuthEventSignupCompleted        AuthAuditEventKind = "signup_completed"
	AuthEventStaffInvited           AuthAuditEventKind = "staff_invited"
	AuthEventSignInSucceeded        AuthAuditEventKind = "sign_in_succeeded"
	AuthEventSignInFailed           AuthAuditEventKind = "sign_in_failed"
	AuthEventSignOut                AuthAuditEventKind = "sign_out"
	AuthEventPasswordResetCompleted AuthAuditEventKind = "password_reset_completed"
	AuthEventEmailChanged           AuthAuditEventKind = "email_changed"
	AuthEventAccountDeleted         AuthAuditEventKind = "account_deleted"
	AuthEventRoleChanged            AuthAuditEventKind = "role_changed"
	AuthEventScopeGranted           AuthAuditEventKind = "scope_granted"
	AuthEventScopeRevoked           AuthAuditEventKind = "scope_revoked"
)

// AuthAuditEvent is the append-only auth audit row. See AUTH.md §13.2.
type AuthAuditEvent struct {
	ID          string
	UserID      string
	ActorUserID string
	Kind        AuthAuditEventKind
	IP          string
	UserAgent   string
	OccurredAt  time.Time
	Metadata    map[string]string
	ExpiresAt   time.Time
}
