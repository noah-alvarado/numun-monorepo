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

// ConferenceStatus mirrors DATA_MODEL.md §2.1.
type ConferenceStatus string

const (
	ConferenceStatusDraft               ConferenceStatus = "draft"
	ConferenceStatusOpenForRegistration ConferenceStatus = "open-for-registration"
	ConferenceStatusClosed              ConferenceStatus = "closed"
	ConferenceStatusInProgress          ConferenceStatus = "in-progress"
	ConferenceStatusArchived            ConferenceStatus = "archived"
)

// Conference is a multi-year scope row.
type Conference struct {
	ID            string
	Name          string
	EditionNumber int
	Year          int
	StartsAt      time.Time
	EndsAt        time.Time
	Status        ConferenceStatus
	Metadata      map[string]string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// DelegationStatus mirrors DATA_MODEL.md §2.3.
type DelegationStatus string

const (
	DelegationStatusPending  DelegationStatus = "pending"
	DelegationStatusApproved DelegationStatus = "approved"
	DelegationStatusRejected DelegationStatus = "rejected"
)

// Trinary is the preference value used per committee axis. See ASSIGNMENT_ALGORITHM.md §4.1.
type Trinary string

const (
	TrinaryUnspecified Trinary = ""
	TrinaryPositive    Trinary = "positive"
	TrinaryNegative    Trinary = "negative"
	TrinaryNeutral     Trinary = "neutral"
)

// CommitteePreferences carries the trinary per-axis matrix on a Delegation.
type CommitteePreferences struct {
	TypeCrisis    Trinary
	TypeNonCrisis Trinary
	SizeSmall     Trinary
	SizeMedium    Trinary
	SizeLarge     Trinary
}

// Address — postal address carried by Delegation.
type Address struct {
	Street     string
	City       string
	State      string
	PostalCode string
	Country    string
}

// EstimatedDelegates — registration-time guesstimate. See DATA_MODEL.md §2.3.
type EstimatedDelegates struct {
	Total                 int
	FinanciallyQualifying int
}

// Delegation is a school's per-conference registration.
type Delegation struct {
	ID                   string
	ConferenceID         string
	School               string
	Address              Address
	Status               DelegationStatus
	EstimatedDelegates   EstimatedDelegates
	CommitteePreferences CommitteePreferences

	// Denormalized payment summary. Authoritative ledger is PaymentRecord.
	BalanceDueUnits int64
	BalanceDueCents int32
	PaidInFull      bool

	ApprovedAt time.Time
	ApprovedBy string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// AdvisorRole mirrors DelegationAdvisor.role from DATA_MODEL.md §2.5.
type AdvisorRole string

const (
	AdvisorRoleLead      AdvisorRole = "lead"
	AdvisorRoleSecondary AdvisorRole = "secondary"
)

// DelegationAdvisor links a User to a Delegation. See DATA_MODEL.md §2.5.
type DelegationAdvisor struct {
	UserID       string
	DelegationID string
	ConferenceID string
	Role         AdvisorRole

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// StaffDelegationAssignment — DATA_MODEL.md §2.6.
type StaffDelegationAssignment struct {
	UserID       string
	DelegationID string
	ConferenceID string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// StaffCommitteeAssignment — DATA_MODEL.md §2.7.
type StaffCommitteeAssignment struct {
	UserID       string
	CommitteeID  string
	ConferenceID string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
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

	// Delegation lifecycle.
	AuthEventDelegationApproved AuthAuditEventKind = "delegation_approved"
	AuthEventDelegationRejected AuthAuditEventKind = "delegation_rejected"
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
