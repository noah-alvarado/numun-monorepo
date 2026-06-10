package domain

import "time"

// AwardRecipientKind — what sort of entity is being honored. M11 expanded
// DATA_MODEL.md §2.14 beyond delegate/delegation to also include committees,
// users (covers staffers and advisors), and conferences. A delegate-pair is
// modeled as two recipients of kind=Delegate on the same Award.
type AwardRecipientKind string

const (
	AwardRecipientKindDelegate   AwardRecipientKind = "delegate"
	AwardRecipientKindDelegation AwardRecipientKind = "delegation"
	AwardRecipientKindCommittee  AwardRecipientKind = "committee"
	AwardRecipientKindUser       AwardRecipientKind = "user"
	AwardRecipientKindConference AwardRecipientKind = "conference"
)

// AwardRecipient — one honored entity on an Award. DisplayName is the
// denormalized human label captured at write time so the CMS markdown and
// public site can render without re-fetching the API.
type AwardRecipient struct {
	Kind        AwardRecipientKind
	ID          string
	DisplayName string
}

// Award — DATA_MODEL.md §2.14 (expanded for M11).
type Award struct {
	ID           string
	ConferenceID string
	Name         string
	Category     string
	Recipients   []AwardRecipient
	AwardedAt    time.Time
	AwardedBy    string

	IsDeleted bool
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy string
	UpdatedBy string
}
