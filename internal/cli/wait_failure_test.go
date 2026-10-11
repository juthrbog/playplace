package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// postCreationFaults makes one step fail after AWS created the account, or
// makes the creation poll itself fail.
type postCreationFaults struct {
	core.Provider
	statusErr, readErr, placeErr, grantErr error
	bare                                   bool // drop the creation tags, as an emulator can
}

func (p *postCreationFaults) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	if p.bare {
		req.Tags = nil
	}
	return p.Provider.RequestAccount(ctx, req)
}

func (p *postCreationFaults) CreateStatus(ctx context.Context, id string) (core.CreateRequest, error) {
	if p.statusErr != nil {
		return core.CreateRequest{}, p.statusErr
	}
	return p.Provider.CreateStatus(ctx, id)
}

func (p *postCreationFaults) GetAccount(ctx context.Context, id string) (core.RemoteAccount, error) {
	if p.readErr != nil {
		return core.RemoteAccount{}, p.readErr
	}
	return p.Provider.GetAccount(ctx, id)
}

func (p *postCreationFaults) PlaceAccount(ctx context.Context, id string, tags map[string]string) error {
	if p.placeErr != nil {
		return p.placeErr
	}
	return p.Provider.PlaceAccount(ctx, id, tags)
}

func (p *postCreationFaults) GrantAccess(ctx context.Context, id string, user core.User) error {
	if p.grantErr != nil {
		return p.grantErr
	}
	return p.Provider.GrantAccess(ctx, id, user)
}

// When a step after the creation fails, approve and create still fail. The
// error says that AWS created the account, with its ID, whether the account
// is placed in the Playground OU, and whether a refresh retries the step.
// The cause comes last. The test then checks these claims: the account is
// where the error says, and a refresh without the fault finishes the
// handoff, or leaves the account alone. A failure of the creation itself
// keeps its own error.
func TestWaitForAccountSaysThatTheAccountExists(t *testing.T) {
	previousPoll := createPoll
	createPoll = time.Millisecond
	defer func() { createPoll = previousPoll }()
	unavailable := errors.New("unavailable")
	for _, tc := range []struct {
		name  string
		fault func(*fake.Provider, *postCreationFaults)
		want  string // the error, with {id} for the account ID
		where string // where the account is after the wait: ou, root, or none
	}{
		{
			name: "fail-budget",
			fault: func(prov *fake.Provider, _ *postCreationFaults) {
				prov.BudgetFailure = "describe budget: StatusCode: 501"
			},
			want:  "fail-budget was created as account {id} and placed in the Playground OU. The next refresh retries the step that failed: budget for fail-budget: describe budget: StatusCode: 501",
			where: "ou",
		},
		{
			name:  "fail-grant",
			fault: func(_ *fake.Provider, p *postCreationFaults) { p.grantErr = unavailable },
			want:  "fail-grant was created as account {id} and placed in the Playground OU. The next refresh retries the step that failed: grant fail-grant: unavailable",
			where: "ou",
		},
		{
			name:  "fail-place",
			fault: func(_ *fake.Provider, p *postCreationFaults) { p.placeErr = unavailable },
			want:  "fail-place was created as account {id}, but its placement in the Playground OU did not finish. Unless someone moved the account to another OU, the next refresh retries it: place fail-place: unavailable",
			where: "root",
		},
		{
			name:  "fail-read",
			fault: func(_ *fake.Provider, p *postCreationFaults) { p.readErr = unavailable },
			want:  "fail-read was created as account {id}, but its placement in the Playground OU did not finish. Unless someone moved the account to another OU, the next refresh retries it: unavailable",
			where: "root",
		},
		{
			name:  "fail-bare",
			fault: func(_ *fake.Provider, p *postCreationFaults) { p.bare = true },
			want:  "fail-bare was created as account {id}, but playplace did not place it in the Playground OU. A refresh does not place it either: account fail-bare ({id}) has no playplace creation tags, so playplace does not place or manage it",
			where: "root",
		},
		{
			name:  "fail-create",
			fault: func(prov *fake.Provider, _ *postCreationFaults) { prov.FailNext = "EMAIL_ALREADY_EXISTS" },
			want:  "fail-create is failed: EMAIL_ALREADY_EXISTS",
			where: "none",
		},
		{
			name:  "fail-status",
			fault: func(_ *fake.Provider, p *postCreationFaults) { p.statusErr = unavailable },
			want:  "unavailable",
			where: "none",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := fake.New()
			prov.PollsToDone = 1
			prov.AccessOn = true
			prov.AddUser("user1", "owner", "owner@example.com")
			p := &postCreationFaults{Provider: prov}
			tc.fault(prov, p)
			svc := core.NewService(p, nil, core.DefaultConfig(), nil)
			ctx := context.Background()
			acc, err := svc.Request(ctx, core.RequestInput{Name: tc.name, Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = captureStdout(t, func() error { return waitForAccount(ctx, &app{svc: svc}, acc, time.Minute) })
			created, _ := prov.CreateStatus(ctx, acc.RequestID)
			id := created.ProviderID
			if want := strings.ReplaceAll(tc.want, "{id}", id); err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant  %s", err, want)
			}
			if got := location(t, prov, id); got != tc.where {
				t.Fatalf("account %q is in %s, want %s", id, got, tc.where)
			}
			if tc.where == "none" {
				return
			}
			*p = postCreationFaults{Provider: prov, bare: p.bare}
			prov.BudgetFailure = ""
			if _, err := svc.Refresh(ctx); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			handoff := prov.Tags(id)[core.TagHandoff]
			if p.bare {
				if got := location(t, prov, id); got != "root" || handoff != "" {
					t.Fatalf("a refresh moved or tagged an account without creation tags: %s, handoff %q", got, handoff)
				}
				return
			}
			if got := location(t, prov, id); got != "ou" || handoff != "complete" {
				t.Fatalf("the refresh did not retry the failed step: account in %s, handoff %q", got, handoff)
			}
		})
	}
}

// location says whether the fake has account id in the Playground OU, in
// the organization root, or nowhere.
func location(t *testing.T, prov *fake.Provider, id string) string {
	t.Helper()
	has := func(list []core.RemoteAccount) bool {
		return slices.ContainsFunc(list, func(r core.RemoteAccount) bool { return id != "" && r.ProviderID == id })
	}
	ctx := context.Background()
	ou, err := prov.ListPlaygroundAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root, err := prov.ListRootAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case has(ou):
		return "ou"
	case has(root):
		return "root"
	}
	return "none"
}
