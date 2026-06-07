// Package handlers wires the Connect-go service implementations to the
// domain layer (auth + store). Each *.go file in this package implements one
// service.
//
// scope-check: skip
//
// (This file contains shared mapping helpers, not handler methods, so the
// scope-check grep gate is opted out for it.)
package handlers

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/numun/numun/api/internal/domain"
	usersv1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

// protoRole maps domain.Role to the proto enum.
func protoRole(r domain.Role) usersv1.User_Role {
	switch r {
	case domain.RoleAdvisor:
		return usersv1.User_ROLE_ADVISOR
	case domain.RoleStaffStaffer:
		return usersv1.User_ROLE_STAFF_STAFFER
	case domain.RoleStaffAdmin:
		return usersv1.User_ROLE_STAFF_ADMIN
	}
	return usersv1.User_ROLE_UNSPECIFIED
}

// domainRole maps the proto enum back to domain.Role. Returns ("", false) for
// unspecified.
func domainRole(r usersv1.User_Role) (domain.Role, bool) {
	switch r {
	case usersv1.User_ROLE_ADVISOR:
		return domain.RoleAdvisor, true
	case usersv1.User_ROLE_STAFF_STAFFER:
		return domain.RoleStaffStaffer, true
	case usersv1.User_ROLE_STAFF_ADMIN:
		return domain.RoleStaffAdmin, true
	}
	return "", false
}

func protoEmailStatus(s domain.EmailStatus) usersv1.User_EmailStatus {
	switch s {
	case domain.EmailStatusOK:
		return usersv1.User_EMAIL_STATUS_OK
	case domain.EmailStatusBounced:
		return usersv1.User_EMAIL_STATUS_BOUNCED
	case domain.EmailStatusComplained:
		return usersv1.User_EMAIL_STATUS_COMPLAINED
	}
	return usersv1.User_EMAIL_STATUS_UNSPECIFIED
}

func userToProto(u domain.User) *usersv1.User {
	return &usersv1.User{
		Id:                 u.ID,
		Role:               protoRole(u.Role),
		Email:              u.Email,
		Name:               u.Name,
		Phone:              u.Phone,
		EmailStatus:        protoEmailStatus(u.EmailStatus),
		AnnouncementsOptIn: u.AnnouncementsOptIn,
		Version:            int32(u.Version),
		CreatedAt:          tsOrNil(u.CreatedAt),
		UpdatedAt:          tsOrNil(u.UpdatedAt),
	}
}

func tsOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
