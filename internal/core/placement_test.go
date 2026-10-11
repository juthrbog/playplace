package core_test

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

// placementFaults injects placement failures and can hide creation requests,
// as AWS does once a creation is old enough. It can also hide the root's
// accounts, as AWS does for a moment after a creation, or fail to list them.
type placementFaults struct {
	core.Provider
	failPlace     bool
	forgetCreates bool
	hideRoot      bool
	failRoot      bool
	// ouUnderRoot also lists the OU's accounts under the root, as AWS can
	// for a moment after a move.
	ouUnderRoot bool
	places      int
	// afterRootList runs once, right after the root is listed.
	afterRootList func()
	// afterGet runs once, right after an account is read.
	afterGet func()
}

func (p *placementFaults) GetAccount(ctx context.Context, id string) (core.RemoteAccount, error) {
	out, err := p.Provider.GetAccount(ctx, id)
	if hook := p.afterGet; hook != nil {
		p.afterGet = nil
		hook()
	}
	return out, err
}

func (p *placementFaults) PlaceAccount(ctx context.Context, id string, tags map[string]string) error {
	p.places++
	if p.failPlace {
		return errors.New("AccessDeniedException: organizations:MoveAccount")
	}
	return p.Provider.PlaceAccount(ctx, id, tags)
}

func (p *placementFaults) ListCreateRequests(ctx context.Context) ([]core.CreateRequest, error) {
	if p.forgetCreates {
		return nil, nil
	}
	return p.Provider.ListCreateRequests(ctx)
}

func (p *placementFaults) ListRootAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	if p.failRoot {
		return nil, errors.New("ThrottlingException: rate exceeded")
	}
	out, err := p.Provider.ListRootAccounts(ctx)
	if p.hideRoot {
		out = nil
	}
	if p.ouUnderRoot {
		ou, ouErr := p.ListPlaygroundAccounts(ctx)
		if ouErr != nil {
			return nil, ouErr
		}
		out = append(out, ou...)
	}
	if hook := p.afterRootList; hook != nil {
		p.afterRootList = nil
		hook()
	}
	return out, err
}

func inOU(t *testing.T, h *harness, id string) bool {
	t.Helper()
	accounts, err := h.prov.ListPlaygroundAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a.ProviderID == id {
			return true
		}
	}
	return false
}

// An operator contains a misused account by moving it out of the OU. No
// later pass, poll or command may move it back.
func TestPlacedAccountMovedOutIsNeverMovedBack(t *testing.T) {
	for _, dest := range []string{"ou-quarantine", fake.RootID} {
		t.Run(dest, func(t *testing.T) {
			h := newHarness(t)
			p := &placementFaults{Provider: h.prov}
			h.svc = readinessService(h, p)
			ctx := context.Background()
			r, err := h.svc.Request(ctx, core.RequestInput{Name: "dev-q", Owner: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			a, err := h.svc.PollCreate(ctx, r.RequestID)
			if err != nil || a.Status != core.StatusActive {
				t.Fatalf("poll: %+v %v", a, err)
			}
			h.prov.MoveAccount(a.ProviderID, dest)
			p.places = 0
			h.advance(2 * 24 * time.Hour)
			sum, err := h.svc.Refresh(ctx)
			if err != nil || len(sum.Placed) != 0 || p.places != 0 || inOU(t, h, a.ProviderID) {
				t.Fatalf("moved back: %s err=%v places=%d", sum, err, p.places)
			}
			if _, err := h.svc.PollCreate(ctx, r.RequestID); err != nil || p.places != 0 || inOU(t, h, a.ProviderID) {
				t.Fatalf("poll moved it back: %v places=%d", err, p.places)
			}
			inv, err := h.svc.Inventory(ctx, true)
			if err != nil || len(inv) != 0 {
				t.Fatalf("an account outside the OU is no longer a playground: %+v %v", inv, err)
			}
		})
	}
}

// Someone moved a fresh account out of the root before playplace placed it.
func TestCreationMovedOutBeforePlacementIsLeftAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r, err := h.svc.Request(ctx, core.RequestInput{Name: "taken", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	created, _ := h.prov.CreateStatus(ctx, r.RequestID)
	h.prov.MoveAccount(created.ProviderID, "ou-elsewhere")
	if sum, err := h.svc.Refresh(ctx); err != nil || len(sum.Placed) != 0 {
		t.Fatalf("refresh: %s %v", sum, err)
	}
	if _, err := h.svc.PollCreate(ctx, r.RequestID); err == nil || !strings.Contains(err.Error(), "not the organization root") {
		t.Fatalf("poll should refuse to move it: %v", err)
	}
	if inOU(t, h, created.ProviderID) {
		t.Fatal("account was moved out of another OU")
	}
}

// A creation that cannot be placed stays visible and keeps failing the pass
// for as long as it waits in the root, even after AWS forgets the request.
func TestUnplacedCreationNeverGoesQuiet(t *testing.T) {
	h := newHarness(t)
	p := &placementFaults{Provider: h.prov, failPlace: true}
	h.svc = readinessService(h, p)
	ctx := context.Background()
	r, err := h.svc.Request(ctx, core.RequestInput{Name: "stuck", Owner: "owner", BudgetUSD: 30})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	for _, day := range []int{0, 2, 8, 30, 120} {
		h.now = t0.Add(time.Duration(day) * 24 * time.Hour)
		p.forgetCreates = day >= 120
		_, err := h.svc.Refresh(ctx)
		if err == nil || !strings.Contains(err.Error(), "place stuck") {
			t.Fatalf("day %d: placement failure went quiet: %v", day, err)
		}
		inv, err := h.svc.Inventory(ctx, true)
		if err != nil || len(inv) != 1 {
			t.Fatalf("day %d: inventory = %+v %v", day, inv, err)
		}
		a := inv[0]
		if a.Status != core.StatusCreating || a.Owner != "owner" || a.BudgetUSD != 30 || a.ProviderID == "" {
			t.Fatalf("day %d: row = %+v", day, a)
		}
		if (a.LastError == "") != (day == 0) {
			t.Fatalf("day %d: stuck warning = %q", day, a.LastError)
		}
		if wantID := map[bool]string{false: r.RequestID, true: a.ProviderID}[p.forgetCreates]; a.ID != wantID {
			t.Fatalf("day %d: id = %s, want %s", day, a.ID, wantID)
		}
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "stuck", Owner: "other"}); err == nil {
		t.Fatal("an unplaced account still holds its name")
	}
	p.failPlace = false
	sum, err := h.svc.Refresh(ctx)
	if len(sum.Placed) != 1 {
		t.Fatalf("placement did not resume: %s %v", sum, err)
	}
	// The approved expiry passed while the account waited in the root, so
	// the pass that places it also closes it. The docs say so.
	if !slices.Contains(sum.Closed, "stuck") {
		t.Fatalf("late placement after expiry did not close the account: %s %v", sum, err)
	}
}

// After one hour the inventory row explains the wait, without claiming that
// placement failed: with a slow reconcile schedule, nothing tried it yet.
func TestUnplacedCreationNoteDoesNotClaimFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "slow", Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	h.advance(2 * time.Hour)
	inv, err := h.svc.Inventory(ctx, true)
	if err != nil || len(inv) != 1 {
		t.Fatalf("inventory = %+v %v", inv, err)
	}
	note := inv[0].LastError
	for _, want := range []string{"waits outside the Playground OU for the next reconcile", "no budget, OU guardrail, or expiry"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note = %q, want %q", note, want)
		}
	}
	if strings.Contains(note, "fail") {
		t.Fatalf("note claims a failure: %q", note)
	}
}

// The root listing only adds unplaced creations. When it fails, every UI
// read still lists the OU, and the reconcile pass reports the failure.
func TestRootListingFailureKeepsInventoryAndFailsRefresh(t *testing.T) {
	h := newHarness(t)
	p := &placementFaults{Provider: h.prov, failRoot: true}
	h.svc = readinessService(h, p)
	ctx := context.Background()
	h.prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "in-ou", JoinedAt: t0, Tags: map[string]string{
		core.TagManaged: "true", core.TagOwner: "owner", core.TagBudget: "50", core.TagExpires: t0.Add(72 * time.Hour).Format(time.RFC3339),
	}})
	inv, err := h.svc.Inventory(ctx, true)
	if err != nil || len(inv) != 1 || inv[0].Name != "in-ou" {
		t.Fatalf("inventory failed with the root listing: %+v %v", inv, err)
	}
	if _, err := h.svc.Refresh(ctx); err == nil || !strings.Contains(err.Error(), "list root accounts") {
		t.Fatalf("refresh hid the root listing failure: %v", err)
	}
}

// AWS can report a creation done before it lists the account. The name must
// stay taken meanwhile, so a second request cannot reach AWS with it.
func TestFinishedCreationHoldsItsNameBeforeAWSListsIt(t *testing.T) {
	h := newHarness(t)
	p := &placementFaults{Provider: h.prov, hideRoot: true}
	h.svc = readinessService(h, p)
	ctx := context.Background()
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "lagging", Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	inv, err := h.svc.Inventory(ctx, true)
	if err != nil || len(inv) != 1 || inv[0].Status != core.StatusCreating || inv[0].Name != "lagging" {
		t.Fatalf("inventory = %+v %v", inv, err)
	}
	if _, err := h.svc.Request(ctx, core.RequestInput{Name: "lagging", Owner: "other", Email: "other@example.com"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second request with the same name: %v", err)
	}
}

// Another process can place the account and finish its handoff after this
// pass listed the root. The stale listing must not repeat the handoff.
func TestStaleRootListingDoesNotRepeatTheHandoff(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cli := h.svc
	r, err := cli.Request(ctx, core.RequestInput{Name: "race", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	p := &placementFaults{Provider: h.prov}
	worker := readinessService(h, p)
	p.afterRootList = func() {
		// The CLI approve loop places the account and hands it off.
		if a, err := cli.PollCreate(ctx, r.RequestID); err != nil || a.Status != core.StatusActive {
			t.Fatalf("cli poll: %+v %v", a, err)
		}
	}
	if _, err := worker.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	created, _ := h.prov.CreateStatus(ctx, r.RequestID)
	acct, _ := h.prov.GetAccount(ctx, created.ProviderID)
	if acct.Tags[core.TagHandoff] != "complete" || readyNotices(h) != 1 {
		t.Fatalf("handoff = %q, ready notices = %d, want complete and 1", acct.Tags[core.TagHandoff], readyNotices(h))
	}
}

// The mirror case: another process places the account and finishes its
// handoff after a poll read the account, but before the poll places it.
func TestStalePollReadDoesNotRepeatTheHandoff(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	audit := &recorderAudit{}
	worker := h.svc
	worker.SetAuditor(audit)
	r, err := worker.Request(ctx, core.RequestInput{Name: "race", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	p := &placementFaults{Provider: h.prov}
	cli := readinessService(h, p)
	cli.SetAuditor(audit)
	p.afterGet = func() {
		// The serve worker places the account and hands it off.
		if sum, err := worker.Refresh(ctx); err != nil || len(sum.Placed) != 1 {
			t.Fatalf("worker refresh: %s %v", sum, err)
		}
	}
	if a, err := cli.PollCreate(ctx, r.RequestID); err != nil || a.Status != core.StatusActive {
		t.Fatalf("cli poll: %+v %v", a, err)
	}
	created, _ := h.prov.CreateStatus(ctx, r.RequestID)
	acct, _ := h.prov.GetAccount(ctx, created.ProviderID)
	placed := 0
	for _, kind := range audit.kinds() {
		if kind == "placed" {
			placed++
		}
	}
	if acct.Tags[core.TagHandoff] != "complete" || readyNotices(h) != 1 || placed != 1 {
		t.Fatalf("handoff = %q, ready notices = %d, placed events = %d, want complete, 1 and 1", acct.Tags[core.TagHandoff], readyNotices(h), placed)
	}
}

// AWS can list an account under the root and under the OU for a moment
// after a move. Such an account is handled once, as an OU member. A root
// account with the placing marker but without the managed tag is not a
// playplace creation, so nothing places or shows it.
func TestAccountListedUnderRootAndOUIsHandledOnce(t *testing.T) {
	h := newHarness(t)
	p := &placementFaults{Provider: h.prov, ouUnderRoot: true}
	h.svc = readinessService(h, p)
	ctx := context.Background()
	r, err := h.svc.Request(ctx, core.RequestInput{Name: "lag", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	created, _ := h.prov.CreateStatus(ctx, r.RequestID)
	// A placement moved it, but did not write the handoff yet.
	h.prov.MoveAccount(created.ProviderID, fake.OUID)
	h.prov.Seed(core.RemoteAccount{ProviderID: "111111111111", Name: "foreign", JoinedAt: t0.Add(-48 * time.Hour), Tags: map[string]string{core.TagHandoff: "placing"}})
	h.prov.MoveAccount("111111111111", fake.RootID)
	inv, err := h.svc.Inventory(ctx, true)
	if err != nil || len(inv) != 1 || inv[0].Name != "lag" || inv[0].Status != core.StatusActive {
		t.Fatalf("inventory = %+v %v", inv, err)
	}
	if sum, err := h.svc.Refresh(ctx); err != nil || len(sum.Placed) != 0 || p.places != 0 || readyNotices(h) != 1 {
		t.Fatalf("refresh: %s err=%v places=%d ready notices=%d, want no placement and 1 notice", sum, err, p.places, readyNotices(h))
	}
}

// AWS reports a new account as PENDING_ACTIVATION for a moment. Within the
// placement grace that is a creation, not a provider state to investigate.
func TestPendingActivationCreationShowsAsCreating(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r, err := h.svc.Request(ctx, core.RequestInput{Name: "fresh", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	created, _ := h.prov.CreateStatus(ctx, r.RequestID)
	acct, _ := h.prov.GetAccount(ctx, created.ProviderID)
	acct.Status = "PENDING_ACTIVATION"
	h.prov.Seed(acct)
	h.prov.MoveAccount(acct.ProviderID, fake.RootID)
	inv, err := h.svc.Inventory(ctx, true)
	if err != nil || len(inv) != 1 || inv[0].Name != "fresh" || inv[0].Status != core.StatusCreating || inv[0].LastError != "" {
		t.Fatalf("within grace: inventory = %+v %v", inv, err)
	}
	if sum, err := h.svc.Refresh(ctx); err != nil || len(sum.Placed) != 0 {
		t.Fatalf("within grace: refresh = %s %v", sum, err)
	}
	h.advance(2 * time.Hour)
	inv, err = h.svc.Inventory(ctx, true)
	if err != nil || len(inv) != 1 || inv[0].Status != core.StatusUnavailable || !strings.Contains(inv[0].LastError, "PENDING_ACTIVATION") {
		t.Fatalf("after grace: inventory = %+v %v", inv, err)
	}
}

// An account without the creation tags, for example from an emulator that
// drops them, is not reported as an active playground.
func TestPollingAnUntaggedCreationFails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id, err := h.prov.RequestAccount(ctx, core.AccountRequest{Name: "untagged", Email: "u@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	if a, err := h.svc.PollCreate(ctx, id); err == nil || !strings.Contains(err.Error(), "no playplace creation tags") {
		t.Fatalf("poll = %+v %v", a, err)
	}
	created, _ := h.prov.CreateStatus(ctx, id)
	if inOU(t, h, created.ProviderID) {
		t.Fatal("untagged account was placed")
	}
}

// Other tooling's creations, legacy accounts without the placement marker,
// and closed creations are not placed, shown, or reported.
func TestRootAccountsOtherThanUnplacedCreationsAreIgnored(t *testing.T) {
	h := newHarness(t)
	p := &placementFaults{Provider: h.prov}
	h.svc = readinessService(h, p)
	ctx := context.Background()
	if _, err := h.prov.RequestAccount(ctx, core.AccountRequest{Name: "control-tower", Email: "ct@example.com"}); err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	for id, tags := range map[string]map[string]string{
		"111111111111": {core.TagManaged: "true", core.TagOwner: "legacy", core.TagBudget: "50", core.TagExpires: t0.Add(time.Hour).Format(time.RFC3339)},
		"222222222222": {core.TagManaged: "true", core.TagHandoff: "placing", core.TagOwner: "gone", core.TagBudget: "50", core.TagExpires: t0.Add(time.Hour).Format(time.RFC3339)},
	} {
		acct := core.RemoteAccount{ProviderID: id, Name: "acct-" + id[:1], Tags: tags, JoinedAt: t0.Add(-48 * time.Hour)}
		if id == "222222222222" {
			acct.Status = "CLOSED"
		}
		h.prov.Seed(acct)
		h.prov.MoveAccount(id, fake.RootID)
	}
	sum, err := h.svc.Refresh(ctx)
	if err != nil || !sum.Empty() || p.places != 0 {
		t.Fatalf("refresh acted on foreign root accounts: %s %v places=%d", sum, err, p.places)
	}
	inv, err := h.svc.Inventory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range inv {
		if a.Status != core.StatusClosed {
			t.Fatalf("foreign or legacy root account listed: %+v", a)
		}
	}
	if len(inv) != 1 {
		t.Fatalf("inventory = %d rows, want only the closed creation", len(inv))
	}
}

// A creation that AWS left unusable is reported once it is overdue.
func TestUnplacedUnavailableCreationIsReportedAfterGrace(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r, err := h.svc.Request(ctx, core.RequestInput{Name: "suspended", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	h.prov.Settle()
	created, _ := h.prov.CreateStatus(ctx, r.RequestID)
	acct, _ := h.prov.GetAccount(ctx, created.ProviderID)
	acct.Status = "SUSPENDED"
	h.prov.Seed(acct)
	h.prov.MoveAccount(acct.ProviderID, fake.RootID)
	if _, err := h.svc.Refresh(ctx); err != nil {
		t.Fatalf("within grace: %v", err)
	}
	h.advance(2 * time.Hour)
	if _, err := h.svc.Refresh(ctx); err == nil || !strings.Contains(err.Error(), "SUSPENDED in the organization root") {
		t.Fatalf("after grace: %v", err)
	}
	inv, _ := h.svc.Inventory(ctx, true)
	if len(inv) != 1 || inv[0].Status != core.StatusUnavailable {
		t.Fatalf("inventory = %+v", inv)
	}
}
