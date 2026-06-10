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

// ExperienceLevel — Delegate's experience level. See DATA_MODEL.md §2.4.
type ExperienceLevel string

const (
	ExperienceLevelNovice       ExperienceLevel = "novice"
	ExperienceLevelIntermediate ExperienceLevel = "intermediate"
	ExperienceLevelAdvanced     ExperienceLevel = "advanced"
)

// Delegate — a student in a Delegation. No login; pure data record.
// See DATA_MODEL.md §2.4.
type Delegate struct {
	ID              string
	ConferenceID    string
	DelegationID    string
	FirstName       string
	LastName        string
	Email           string
	ExperienceLevel ExperienceLevel
	CheckedInAt     time.Time

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// UpsertMode — bulk-import mode. See BULK_IMPORT.md §6.2.
type UpsertMode string

const (
	UpsertModeAdditive UpsertMode = "additive"
	UpsertModeFullSync UpsertMode = "full_sync"
)

// BulkImportSourceType — DATA_MODEL.md §2.17.
type BulkImportSourceType string

const (
	BulkImportSourceCSV         BulkImportSourceType = "csv"
	BulkImportSourceXLSX        BulkImportSourceType = "xlsx"
	BulkImportSourceGoogleSheet BulkImportSourceType = "google_sheet"
)

// BulkImportPreview — short-lived parsed-row cache. DATA_MODEL.md §2.17 and
// BULK_IMPORT.md §11.1. ParsedRows + Summary are stored as opaque JSON blobs
// because the preview-row shape is a proto oneof; the store layer treats
// them as []byte and the handler marshals/unmarshals to the generated type.
type BulkImportPreview struct {
	ID            string
	UserID        string
	DelegationID  string
	ConferenceID  string
	SourceType    BulkImportSourceType
	SourceRef     string
	TabName       string
	ParsedRowsRaw []byte
	SummaryRaw    []byte
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// BulkImportJobStatus — DATA_MODEL.md §2.18.
type BulkImportJobStatus string

const (
	BulkImportJobApplying BulkImportJobStatus = "applying"
	BulkImportJobComplete BulkImportJobStatus = "complete"
	BulkImportJobFailed   BulkImportJobStatus = "failed"
)

// BulkImportJob — recovery / progress tracker for >100-op imports.
// DATA_MODEL.md §2.18 and BULK_IMPORT.md §6.4.
type BulkImportJob struct {
	ID               string
	UploadID         string
	UserID           string
	DelegationID     string
	ConferenceID     string
	Mode             UpsertMode
	TotalBatches     int
	CompletedBatches int
	Status           BulkImportJobStatus
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ExpiresAt        time.Time
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

// PaymentKind mirrors DATA_MODEL.md §2.12. Determines the sign the handler
// applies to the stored amount.
type PaymentKind string

const (
	PaymentKindCharge     PaymentKind = "charge"
	PaymentKindPayment    PaymentKind = "payment"
	PaymentKindAdjustment PaymentKind = "adjustment"
)

// PaymentMethod mirrors DATA_MODEL.md §2.12.
type PaymentMethod string

const (
	PaymentMethodCheck PaymentMethod = "check"
	PaymentMethodWire  PaymentMethod = "wire"
	PaymentMethodCash  PaymentMethod = "cash"
	PaymentMethodOther PaymentMethod = "other"
)

// PaymentRecord — append-only ledger entry. DATA_MODEL.md §2.12.
// AmountUnits + AmountCents are SIGNED (negative for charges, positive for
// payments); the handler applies the sign based on Kind. AmountCurrency is
// always "USD" in v1 but stored explicitly so future multi-currency support
// doesn't require a migration.
type PaymentRecord struct {
	ID             string
	ConferenceID   string
	DelegationID   string
	AmountCurrency string
	AmountUnits    int64
	AmountCents    int32
	Kind           PaymentKind
	Method         PaymentMethod
	Reference      string
	Notes          string
	RecordedBy     string
	RecordedAt     time.Time

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

	// Bulk import (M6).
	AuthEventBulkImportPreviewed  AuthAuditEventKind = "bulk_import_previewed"
	AuthEventBulkImportCommitted  AuthAuditEventKind = "bulk_import_committed"
	AuthEventBulkDelegatesPresign AuthAuditEventKind = "bulk_delegates_presigned"

	// Delegate lifecycle (M6).
	AuthEventDelegateCreated  AuthAuditEventKind = "delegate_created"
	AuthEventDelegateUpdated  AuthAuditEventKind = "delegate_updated"
	AuthEventDelegateDeleted  AuthAuditEventKind = "delegate_deleted"
	AuthEventDelegateCheckedIn AuthAuditEventKind = "delegate_checked_in"

	// Committee + position lifecycle (M7).
	AuthEventCommitteeCreated AuthAuditEventKind = "committee_created"
	AuthEventCommitteeUpdated AuthAuditEventKind = "committee_updated"
	AuthEventCommitteeDeleted AuthAuditEventKind = "committee_deleted"
	AuthEventPositionCreated  AuthAuditEventKind = "position_created"
	AuthEventPositionUpdated  AuthAuditEventKind = "position_updated"
	AuthEventPositionDeleted  AuthAuditEventKind = "position_deleted"

	// Assignment algorithm lifecycle (M7).
	AuthEventAssignmentRunStarted     AuthAuditEventKind = "assignment_run_started"
	AuthEventAssignmentRunCompleted   AuthAuditEventKind = "assignment_run_completed"
	AuthEventAssignmentApproved       AuthAuditEventKind = "assignment_approved"
	AuthEventAssignmentUnapproved     AuthAuditEventKind = "assignment_unapproved"
	AuthEventAssignmentManuallyEdited AuthAuditEventKind = "assignment_manually_edited"

	// Payment lifecycle (M8).
	AuthEventPaymentRecorded AuthAuditEventKind = "payment_recorded"
	AuthEventPaymentUpdated  AuthAuditEventKind = "payment_updated"
	AuthEventPaymentDeleted  AuthAuditEventKind = "payment_deleted"
)

// CommitteeType — DATA_MODEL.md §2.8.
type CommitteeType string

const (
	CommitteeTypeCrisis    CommitteeType = "crisis"
	CommitteeTypeNonCrisis CommitteeType = "non-crisis"
)

// CommitteeSize — DATA_MODEL.md §2.8.
type CommitteeSize string

const (
	CommitteeSizeSmall  CommitteeSize = "small"
	CommitteeSizeMedium CommitteeSize = "medium"
	CommitteeSizeLarge  CommitteeSize = "large"
)

// PrestigeTier — DATA_MODEL.md §2.9.
type PrestigeTier string

const (
	PrestigeTierStandard PrestigeTier = "standard"
	PrestigeTierElevated PrestigeTier = "elevated"
	PrestigeTierReserved PrestigeTier = "reserved"
)

// Committee — DATA_MODEL.md §2.8.
type Committee struct {
	ID                 string
	ConferenceID       string
	Name               string
	Type               CommitteeType
	Size               CommitteeSize
	BackgroundGuideRef string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// Position — DATA_MODEL.md §2.9.
type Position struct {
	ID             string
	ConferenceID   string
	CommitteeID    string
	Name           string
	MaxDelegates   int
	DualDelegation bool
	PrestigeTier   PrestigeTier

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// AssignmentStatus — DATA_MODEL.md §2.10.
type AssignmentStatus string

const (
	AssignmentStatusProposed AssignmentStatus = "proposed"
	AssignmentStatusApproved AssignmentStatus = "approved"
)

// Assignment — DATA_MODEL.md §2.10.
type Assignment struct {
	ID           string
	ConferenceID string
	DelegateID   string
	PositionID   string
	CommitteeID  string
	DelegationID string

	Status     AssignmentStatus
	ProposedAt time.Time
	ApprovedAt time.Time
	ApprovedBy string
	RunID      string
	Score      float64
	Reason     string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

// AssignmentRunStatus — DATA_MODEL.md §2.11.
type AssignmentRunStatus string

const (
	AssignmentRunStatusRunning AssignmentRunStatus = "running"
	AssignmentRunStatusDone    AssignmentRunStatus = "done"
	AssignmentRunStatusFailed  AssignmentRunStatus = "failed"
)

// AssignmentRun — DATA_MODEL.md §2.11.
type AssignmentRun struct {
	ID              string
	ConferenceID    string
	Seed            uint64
	RunOrdinal      int
	IsCanonical     bool
	TriggeredBy     string
	TriggeredAt     time.Time
	CompletedAt     time.Time
	Status          AssignmentRunStatus
	Objective       float64
	AssignmentCount int
	InputsHash      string
	Diagnostics     string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}

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

// EmailKind enumerates the templates served by /api/templates/email/.
// Mirrors EMAIL.md §1 catalog (T1–T7 + A1) plus the feedback-loop kinds.
type EmailKind string

const (
	EmailKindDelegationApproved      EmailKind = "delegation_approved"
	EmailKindDelegationRejected      EmailKind = "delegation_rejected"
	EmailKindPaymentRecorded         EmailKind = "payment_recorded"
	EmailKindBulkImportCommitted     EmailKind = "bulk_import_committed"
	EmailKindAssignmentRunCompleted  EmailKind = "assignment_run_completed"
	EmailKindScopeRoleChanged        EmailKind = "scope_role_changed"
	EmailKindNewRegistrationSummary  EmailKind = "new_registration_summary"
	EmailKindAnnouncement            EmailKind = "announcement"

	// Feedback / forensic rows (not user-visible).
	EmailKindBounceReceived    EmailKind = "bounce_received"
	EmailKindComplaintReceived EmailKind = "complaint_received"
	EmailKindDeliveryConfirmed EmailKind = "delivery_confirmed"
)

// EmailEventStatus mirrors EMAIL.md §8.
type EmailEventStatus string

const (
	EmailEventStatusSent       EmailEventStatus = "sent"
	EmailEventStatusFailed     EmailEventStatus = "failed"
	EmailEventStatusSkipped    EmailEventStatus = "skipped"
	EmailEventStatusBounce     EmailEventStatus = "bounce_received"
	EmailEventStatusComplaint  EmailEventStatus = "complaint_received"
	EmailEventStatusDelivery   EmailEventStatus = "delivery_confirmed"
)

// EmailEvent is the per-send audit row. 1-year TTL. EMAIL.md §8.
type EmailEvent struct {
	ID             string
	UserID         string // empty for EMAIL_FEEDBACK#<email> rows
	RecipientEmail string
	Kind           EmailKind
	Subject        string
	SenderAddress  string
	SESMessageID   string
	Status         EmailEventStatus
	FailureReason  string
	ClientToken    string
	SentAt         time.Time
	ExpiresAt      time.Time
	Metadata       map[string]string
}

// NotificationDedupeKind enumerates the patterns that share the dedupe row.
// Only `new-registration` exists in v1; the column is here for future reuse.
type NotificationDedupeKind string

const (
	NotificationDedupeNewRegistration NotificationDedupeKind = "new-registration"
)

// NotificationDedupe is the short-lived row used to enforce at-most-one
// notification per window. EMAIL.md §10.2.
type NotificationDedupe struct {
	Kind            NotificationDedupeKind
	ScopeID         string
	WindowStartedAt time.Time
	ExpiresAt       time.Time
}

// Announcement is the persisted record of a sent announcement. EMAIL.md §5.3.
type Announcement struct {
	ID             string
	ConferenceID   string
	Subject        string
	BodyHTML       string
	BodyText       string
	AudienceFilter string // serialized JSON of the filter accepted at send time
	SentBy         string
	SentAt         time.Time
	RecipientCount int

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}
