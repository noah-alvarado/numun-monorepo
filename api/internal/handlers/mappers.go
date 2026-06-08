package handlers

// Mapping helpers between domain entities and the generated proto messages.
//
// scope-check: skip
//
// (This file holds shared conversion helpers and does not implement RPC
// handlers.)

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

// ── Conference ───────────────────────────────────────────────────────────────

func protoConferenceStatus(s domain.ConferenceStatus) v1.Conference_Status {
	switch s {
	case domain.ConferenceStatusDraft:
		return v1.Conference_STATUS_DRAFT
	case domain.ConferenceStatusOpenForRegistration:
		return v1.Conference_STATUS_OPEN_FOR_REGISTRATION
	case domain.ConferenceStatusClosed:
		return v1.Conference_STATUS_CLOSED
	case domain.ConferenceStatusInProgress:
		return v1.Conference_STATUS_IN_PROGRESS
	case domain.ConferenceStatusArchived:
		return v1.Conference_STATUS_ARCHIVED
	}
	return v1.Conference_STATUS_UNSPECIFIED
}

func domainConferenceStatus(s v1.Conference_Status) (domain.ConferenceStatus, bool) {
	switch s {
	case v1.Conference_STATUS_DRAFT:
		return domain.ConferenceStatusDraft, true
	case v1.Conference_STATUS_OPEN_FOR_REGISTRATION:
		return domain.ConferenceStatusOpenForRegistration, true
	case v1.Conference_STATUS_CLOSED:
		return domain.ConferenceStatusClosed, true
	case v1.Conference_STATUS_IN_PROGRESS:
		return domain.ConferenceStatusInProgress, true
	case v1.Conference_STATUS_ARCHIVED:
		return domain.ConferenceStatusArchived, true
	}
	return "", false
}

func conferenceToProto(c domain.Conference) *v1.Conference {
	return &v1.Conference{
		Id:            c.ID,
		Name:          c.Name,
		EditionNumber: int32(c.EditionNumber),
		Year:          int32(c.Year),
		StartsAt:      tsOrNilFor(c.StartsAt),
		EndsAt:        tsOrNilFor(c.EndsAt),
		Status:        protoConferenceStatus(c.Status),
		Metadata:      c.Metadata,
		Version:       int32(c.Version),
		CreatedAt:     tsOrNilFor(c.CreatedAt),
		UpdatedAt:     tsOrNilFor(c.UpdatedAt),
	}
}

// ── Delegation ───────────────────────────────────────────────────────────────

func protoDelegationStatus(s domain.DelegationStatus) v1.Delegation_Status {
	switch s {
	case domain.DelegationStatusPending:
		return v1.Delegation_STATUS_PENDING
	case domain.DelegationStatusApproved:
		return v1.Delegation_STATUS_APPROVED
	case domain.DelegationStatusRejected:
		return v1.Delegation_STATUS_REJECTED
	}
	return v1.Delegation_STATUS_UNSPECIFIED
}

func domainDelegationStatus(s v1.Delegation_Status) (domain.DelegationStatus, bool) {
	switch s {
	case v1.Delegation_STATUS_PENDING:
		return domain.DelegationStatusPending, true
	case v1.Delegation_STATUS_APPROVED:
		return domain.DelegationStatusApproved, true
	case v1.Delegation_STATUS_REJECTED:
		return domain.DelegationStatusRejected, true
	}
	return "", false
}

func protoTrinary(t domain.Trinary) v1.Trinary {
	switch t {
	case domain.TrinaryPositive:
		return v1.Trinary_TRINARY_POSITIVE
	case domain.TrinaryNegative:
		return v1.Trinary_TRINARY_NEGATIVE
	case domain.TrinaryNeutral:
		return v1.Trinary_TRINARY_NEUTRAL
	}
	return v1.Trinary_TRINARY_UNSPECIFIED
}

func domainTrinary(t v1.Trinary) domain.Trinary {
	switch t {
	case v1.Trinary_TRINARY_POSITIVE:
		return domain.TrinaryPositive
	case v1.Trinary_TRINARY_NEGATIVE:
		return domain.TrinaryNegative
	case v1.Trinary_TRINARY_NEUTRAL:
		return domain.TrinaryNeutral
	}
	return domain.TrinaryUnspecified
}

func protoAddress(a domain.Address) *v1.Address {
	if a == (domain.Address{}) {
		return nil
	}
	return &v1.Address{
		Street:     a.Street,
		City:       a.City,
		State:      a.State,
		PostalCode: a.PostalCode,
		Country:    a.Country,
	}
}

func domainAddress(a *v1.Address) domain.Address {
	if a == nil {
		return domain.Address{}
	}
	return domain.Address{
		Street:     a.GetStreet(),
		City:       a.GetCity(),
		State:      a.GetState(),
		PostalCode: a.GetPostalCode(),
		Country:    a.GetCountry(),
	}
}

func protoEstimated(e domain.EstimatedDelegates) *v1.EstimatedDelegates {
	if e == (domain.EstimatedDelegates{}) {
		return nil
	}
	return &v1.EstimatedDelegates{
		Total:                 int32(e.Total),
		FinanciallyQualifying: int32(e.FinanciallyQualifying),
	}
}

func domainEstimated(e *v1.EstimatedDelegates) domain.EstimatedDelegates {
	if e == nil {
		return domain.EstimatedDelegates{}
	}
	return domain.EstimatedDelegates{
		Total:                 int(e.GetTotal()),
		FinanciallyQualifying: int(e.GetFinanciallyQualifying()),
	}
}

func protoPrefs(p domain.CommitteePreferences) *v1.CommitteePreferences {
	if p == (domain.CommitteePreferences{}) {
		return nil
	}
	return &v1.CommitteePreferences{
		Type: &v1.CommitteePreferences_TypePrefs{
			Crisis:    protoTrinary(p.TypeCrisis),
			NonCrisis: protoTrinary(p.TypeNonCrisis),
		},
		Size: &v1.CommitteePreferences_SizePrefs{
			Small:  protoTrinary(p.SizeSmall),
			Medium: protoTrinary(p.SizeMedium),
			Large:  protoTrinary(p.SizeLarge),
		},
	}
}

func domainPrefs(p *v1.CommitteePreferences) domain.CommitteePreferences {
	if p == nil {
		return domain.CommitteePreferences{}
	}
	out := domain.CommitteePreferences{}
	if t := p.GetType(); t != nil {
		out.TypeCrisis = domainTrinary(t.GetCrisis())
		out.TypeNonCrisis = domainTrinary(t.GetNonCrisis())
	}
	if s := p.GetSize(); s != nil {
		out.SizeSmall = domainTrinary(s.GetSmall())
		out.SizeMedium = domainTrinary(s.GetMedium())
		out.SizeLarge = domainTrinary(s.GetLarge())
	}
	return out
}

func delegationToProto(d domain.Delegation) *v1.Delegation {
	out := &v1.Delegation{
		Id:                   d.ID,
		ConferenceId:         d.ConferenceID,
		School:               d.School,
		Address:              protoAddress(d.Address),
		Status:               protoDelegationStatus(d.Status),
		EstimatedDelegates:   protoEstimated(d.EstimatedDelegates),
		CommitteePreferences: protoPrefs(d.CommitteePreferences),
		BalanceDue: &v1.Money{
			Currency: "USD",
			Units:    d.BalanceDueUnits,
			Cents:    d.BalanceDueCents,
		},
		PaidInFull: d.PaidInFull,
		ApprovedAt: tsOrNilFor(d.ApprovedAt),
		ApprovedBy: d.ApprovedBy,
		Version:    int32(d.Version),
		CreatedAt:  tsOrNilFor(d.CreatedAt),
		UpdatedAt:  tsOrNilFor(d.UpdatedAt),
	}
	return out
}

// ── DelegationAdvisor ────────────────────────────────────────────────────────

func protoAdvisorRole(r domain.AdvisorRole) v1.DelegationAdvisor_Role {
	switch r {
	case domain.AdvisorRoleLead:
		return v1.DelegationAdvisor_ROLE_LEAD
	case domain.AdvisorRoleSecondary:
		return v1.DelegationAdvisor_ROLE_SECONDARY
	}
	return v1.DelegationAdvisor_ROLE_UNSPECIFIED
}

func domainAdvisorRole(r v1.DelegationAdvisor_Role) (domain.AdvisorRole, bool) {
	switch r {
	case v1.DelegationAdvisor_ROLE_LEAD:
		return domain.AdvisorRoleLead, true
	case v1.DelegationAdvisor_ROLE_SECONDARY:
		return domain.AdvisorRoleSecondary, true
	}
	return "", false
}

func advisorToProto(a domain.DelegationAdvisor) *v1.DelegationAdvisor {
	return &v1.DelegationAdvisor{
		UserId:       a.UserID,
		DelegationId: a.DelegationID,
		ConferenceId: a.ConferenceID,
		Role:         protoAdvisorRole(a.Role),
		Version:      int32(a.Version),
		CreatedAt:    tsOrNilFor(a.CreatedAt),
		UpdatedAt:    tsOrNilFor(a.UpdatedAt),
	}
}

func tsOrNilFor(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// ── Delegate ─────────────────────────────────────────────────────────────────

func protoExperienceLevel(l domain.ExperienceLevel) v1.ExperienceLevel {
	switch l {
	case domain.ExperienceLevelNovice:
		return v1.ExperienceLevel_EXPERIENCE_LEVEL_NOVICE
	case domain.ExperienceLevelIntermediate:
		return v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE
	case domain.ExperienceLevelAdvanced:
		return v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED
	}
	return v1.ExperienceLevel_EXPERIENCE_LEVEL_UNSPECIFIED
}

func domainExperienceLevel(l v1.ExperienceLevel) domain.ExperienceLevel {
	switch l {
	case v1.ExperienceLevel_EXPERIENCE_LEVEL_NOVICE:
		return domain.ExperienceLevelNovice
	case v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED:
		return domain.ExperienceLevelAdvanced
	case v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE, v1.ExperienceLevel_EXPERIENCE_LEVEL_UNSPECIFIED:
		return domain.ExperienceLevelIntermediate
	}
	return domain.ExperienceLevelIntermediate
}

func delegateToProto(d domain.Delegate) *v1.Delegate {
	return &v1.Delegate{
		Id:              d.ID,
		ConferenceId:    d.ConferenceID,
		DelegationId:    d.DelegationID,
		FirstName:       d.FirstName,
		LastName:        d.LastName,
		Email:           d.Email,
		ExperienceLevel: protoExperienceLevel(d.ExperienceLevel),
		CheckedInAt:     tsOrNilFor(d.CheckedInAt),
		Version:         int32(d.Version),
		CreatedAt:       tsOrNilFor(d.CreatedAt),
		UpdatedAt:       tsOrNilFor(d.UpdatedAt),
	}
}
