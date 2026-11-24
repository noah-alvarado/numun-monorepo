package store_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func TestFindByIDSkipsSoftDeleted(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	created, err := c.CreateCommittee(ctx, domain.Committee{ConferenceID: conferenceID, Name: "DISEC"})
	if err != nil {
		t.Fatalf("create committee: %v", err)
	}
	if _, err := c.FindCommitteeByID(ctx, created.ID); err != nil {
		t.Fatalf("find before delete: %v", err)
	}
	if err := c.SoftDeleteCommittee(ctx, conferenceID, created.ID, created.Version, newID(t)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := c.FindCommitteeByID(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("find after delete: want ErrNotFound, got %v", err)
	}
	if _, err := c.FindCommitteeByID(ctx, newID(t)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("find unknown id: want ErrNotFound, got %v", err)
	}
}

func TestListConferencesPagesNewestFirst(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	var ids []string
	for i := 0; i < 3; i++ {
		conf, err := c.CreateConference(ctx, domain.Conference{Name: "Paging " + newID(t)[:8], Year: 2030 + i})
		if err != nil {
			t.Fatalf("create conference: %v", err)
		}
		ids = append(ids, conf.ID)
	}

	var seen []string
	cursor := ""
	for page := 0; page < 1000; page++ {
		confs, next, err := c.ListConferences(ctx, "", cursor, 2)
		if err != nil {
			t.Fatalf("list conferences: %v", err)
		}
		if len(confs) > 2 {
			t.Fatalf("page size exceeded: %d", len(confs))
		}
		for _, conf := range confs {
			seen = append(seen, conf.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}

	var ours []string
	for _, id := range seen {
		if slices.Contains(ids, id) {
			ours = append(ours, id)
		}
	}
	want := []string{ids[2], ids[1], ids[0]}
	if !slices.Equal(ours, want) {
		t.Fatalf("want %v newest-first, got %v", want, ours)
	}
	if !slices.IsSortedFunc(seen, func(a, b string) int { return strings.Compare(b, a) }) {
		t.Fatalf("listing not newest-first: %v", seen)
	}
}

func TestUserLookupsUseIndex(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	email := "Mixed.Case-" + newID(t) + "@Example.com"
	u, err := c.CreateUser(ctx, domain.User{ID: newID(t), Role: domain.RoleStaffAdmin, Email: email})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	found, err := c.FindUserByEmail(ctx, strings.ToLower(email))
	if err != nil || found.ID != u.ID {
		t.Fatalf("find by email: got %+v, %v", found, err)
	}

	admins, err := c.ListUsersByRole(ctx, domain.RoleStaffAdmin)
	if err != nil {
		t.Fatalf("list by role: %v", err)
	}
	if !slices.ContainsFunc(admins, func(x domain.User) bool { return x.ID == u.ID }) {
		t.Fatalf("staff-admin listing missing %s", u.ID)
	}

	bounced := domain.EmailStatusBounced
	if _, err := c.UpdateUser(ctx, u.ID, u.Version, store.UpdateUserPatch{EmailStatus: &bounced}); err != nil {
		t.Fatalf("mark bounced: %v", err)
	}
	suppressed, err := c.ListSuppressedUsers(ctx)
	if err != nil {
		t.Fatalf("list suppressed: %v", err)
	}
	if !slices.ContainsFunc(suppressed, func(x domain.User) bool { return x.ID == u.ID }) {
		t.Fatalf("suppressed listing missing %s", u.ID)
	}
}

func TestFindEmailEventByClientToken(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	token := newID(t)
	if err := c.RecordEmailEvent(ctx, domain.EmailEvent{
		UserID:         newID(t),
		RecipientEmail: "token@example.com",
		Kind:           domain.EmailKindAnnouncement,
		Status:         domain.EmailEventStatusSent,
		ClientToken:    token,
	}); err != nil {
		t.Fatalf("record email event: %v", err)
	}
	ok, err := c.FindEmailEventByClientToken(ctx, token)
	if err != nil || !ok {
		t.Fatalf("known token: got %v, %v", ok, err)
	}
	ok, err = c.FindEmailEventByClientToken(ctx, newID(t))
	if err != nil || ok {
		t.Fatalf("unknown token: got %v, %v", ok, err)
	}
}
