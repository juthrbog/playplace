package core_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// errAWS stands for an error of the AWS SDK. Its text holds an AWS request
// ID, as the text of every SDK error does.
var errAWS = errors.New("operation error Organizations: ListAccountsForParent, https response error StatusCode: 400, RequestID: 7f3a, api error AccessDeniedException: denied")

// failing is a provider that fails each call named in calls with errAWS.
type failing struct {
	core.Provider
	calls map[string]bool
}

func (f *failing) fail(call string) error {
	if f.calls[call] {
		return errAWS
	}
	return nil
}

func (f *failing) OUTags(ctx context.Context) (map[string]string, error) {
	if err := f.fail("OUTags"); err != nil {
		return nil, err
	}
	return f.Provider.OUTags(ctx)
}

func (f *failing) SetOUTags(ctx context.Context, tags map[string]string) error {
	if err := f.fail("SetOUTags"); err != nil {
		return err
	}
	return f.Provider.SetOUTags(ctx, tags)
}

func (f *failing) RemoveOUTags(ctx context.Context, keys []string) error {
	if err := f.fail("RemoveOUTags"); err != nil {
		return err
	}
	return f.Provider.RemoveOUTags(ctx, keys)
}

func (f *failing) ListPlaygroundAccounts(ctx context.Context) ([]core.RemoteAccount, error) {
	if err := f.fail("ListPlaygroundAccounts"); err != nil {
		return nil, err
	}
	return f.Provider.ListPlaygroundAccounts(ctx)
}

func (f *failing) ListCreateRequests(ctx context.Context) ([]core.CreateRequest, error) {
	if err := f.fail("ListCreateRequests"); err != nil {
		return nil, err
	}
	return f.Provider.ListCreateRequests(ctx)
}

func (f *failing) LookupUser(ctx context.Context, identity string) (core.User, error) {
	if err := f.fail("LookupUser"); err != nil {
		return core.User{}, err
	}
	return f.Provider.LookupUser(ctx, identity)
}

func (f *failing) RequestAccount(ctx context.Context, req core.AccountRequest) (string, error) {
	if err := f.fail("RequestAccount"); err != nil {
		return "", err
	}
	return f.Provider.RequestAccount(ctx, req)
}

func (f *failing) SetTags(ctx context.Context, id string, tags map[string]string) error {
	if err := f.fail("SetTags"); err != nil {
		return err
	}
	return f.Provider.SetTags(ctx, id, tags)
}

func (f *failing) RemoveTags(ctx context.Context, id string, keys []string) error {
	if err := f.fail("RemoveTags"); err != nil {
		return err
	}
	return f.Provider.RemoveTags(ctx, id, keys)
}

func (f *failing) CloseAccount(ctx context.Context, id string) error {
	if err := f.fail("CloseAccount"); err != nil {
		return err
	}
	return f.Provider.CloseAccount(ctx, id)
}

func (f *failing) GetAccount(ctx context.Context, id string) (core.RemoteAccount, error) {
	if err := f.fail("GetAccount"); err != nil {
		return core.RemoteAccount{}, err
	}
	return f.Provider.GetAccount(ctx, id)
}

func (f *failing) Costs(ctx context.Context, id string, from, to time.Time) ([]core.CostPoint, error) {
	if err := f.fail("Costs"); err != nil {
		return nil, err
	}
	return f.Provider.Costs(ctx, id, from, to)
}

func (f *failing) InspectBudget(ctx context.Context, id string, spec core.BudgetSpec) (core.BudgetSnapshot, error) {
	if err := f.fail("InspectBudget"); err != nil {
		return core.BudgetSnapshot{}, err
	}
	return f.Provider.InspectBudget(ctx, id, spec)
}

func (f *failing) EnsureBudget(ctx context.Context, id string, spec core.BudgetSpec) (core.BudgetProtection, error) {
	if err := f.fail("EnsureBudget"); err != nil {
		return core.BudgetProtection{}, err
	}
	return f.Provider.EnsureBudget(ctx, id, spec)
}

// failingService is a second service on the provider of h, with the
// configuration and the clock of h. Its calls fail as f names them.
func failingService(h *harness) (*core.Service, *failing) {
	f := &failing{Provider: h.prov, calls: map[string]bool{}}
	svc := core.NewService(f, h.note, h.svc.Config(), nil)
	svc.Now = func() time.Time { return h.now }
	return svc, f
}

// providerOps lists the Op of every ProviderError in the tree of err, in
// order, so a test can find one in an error that Refresh joined.
func providerOps(err error) []string {
	var out []string
	switch e := err.(type) {
	case *core.ProviderError:
		out = append(out, e.Op)
		out = append(out, providerOps(e.Err)...)
	case interface{ Unwrap() []error }:
		for _, inner := range e.Unwrap() {
			out = append(out, providerOps(inner)...)
		}
	case interface{ Unwrap() error }:
		out = append(out, providerOps(e.Unwrap())...)
	}
	return out
}

// A ProviderError keeps the text of a plain wrap, so logs, the CLI and the
// TUI do not change. The sentinels stay visible through it.
func TestProviderErrorKeepsTheTextAndTheChain(t *testing.T) {
	err := &core.ProviderError{Op: "list playground accounts", Err: errAWS}
	if got, want := err.Error(), "list playground accounts: "+errAWS.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := (&core.ProviderError{Err: errAWS}).Error(); got != errAWS.Error() {
		t.Errorf("Error() with no Op = %q, want the provider text alone", got)
	}
	if !errors.Is(err, errAWS) {
		t.Error("errors.Is does not reach the provider error")
	}
	var pe *core.ProviderError
	if !errors.As(fmt.Errorf("refresh: %w", err), &pe) || pe.Op != "list playground accounts" {
		t.Errorf("errors.As through a wrap = %+v", pe)
	}
	for _, sentinel := range []error{core.ErrNotFound, core.ErrCloseQuota, core.ErrUserNotFound, core.ErrRequestChanged} {
		wrapped := fmt.Errorf("outer: %w", &core.ProviderError{Op: "op", Err: fmt.Errorf("%w: detail", sentinel)})
		if !errors.Is(wrapped, sentinel) {
			t.Errorf("errors.Is(%v) does not see %v through a ProviderError", wrapped, sentinel)
		}
	}
}

// Every failed provider call reaches the caller as a ProviderError, with the
// text of a plain wrap. A refusal of core itself is never one, also under the
// same prefix.
func TestProviderFailuresAreProviderErrorsWithTheSameText(t *testing.T) {
	ctx := context.Background()
	submit := func(t *testing.T, h *harness) core.RequestRef {
		t.Helper()
		q, err := h.svc.SubmitRequest(ctx, core.RequestInput{Name: "box", Owner: "anna@example.com"}, "web")
		if err != nil {
			t.Fatal(err)
		}
		return q.Ref()
	}
	for _, c := range []struct {
		name string
		op   string // the Op of the ProviderError in the error
		want string // the exact text, or a part of it for an error that Refresh joined
		run  func(t *testing.T, h *harness, svc *core.Service, f *failing) error
	}{
		{"list accounts", "list playground accounts", "list playground accounts: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			f.calls["ListPlaygroundAccounts"] = true
			_, err := svc.List(ctx, core.ListFilter{})
			return err
		}},
		{"list creations", "list create requests", "list create requests: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			f.calls["ListCreateRequests"] = true
			_, err := svc.List(ctx, core.ListFilter{})
			return err
		}},
		{"read the queue", "read request queue", "read request queue: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			f.calls["OUTags"] = true
			_, err := svc.GetRequest(ctx, "box")
			return err
		}},
		{"look up the owner", "look up owner", "look up owner: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			h.prov.AccessOn = true
			f.calls["LookupUser"] = true
			_, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "box", Owner: "anna@example.com"}, "web")
			return err
		}},
		{"queue a request", "queue request", "queue request: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			f.calls["SetOUTags"] = true
			_, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "box", Owner: "anna@example.com"}, "web")
			return err
		}},
		{"claim a request", "claim request", "claim request: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			ref := submit(t, h)
			f.calls["SetOUTags"] = true
			_, err := svc.Approve(ctx, ref, "lead@example.com")
			return err
		}},
		{"request the account", "request account", "request account: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			ref := submit(t, h)
			f.calls["RequestAccount"] = true
			_, err := svc.Approve(ctx, ref, "lead@example.com")
			return err
		}},
		{"update a request", "update request", "update request: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			ref := submit(t, h)
			f.calls["SetOUTags"] = true
			_, err := svc.UpdateRequest(ctx, ref, "ops@example.com", core.RequestEdit{BudgetUSD: 70})
			return err
		}},
		{"deny a request", "remove request", "remove request: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			ref := submit(t, h)
			f.calls["RemoveOUTags"] = true
			return svc.Deny(ctx, ref, "lead@example.com", "no")
		}},
		{"withdraw a request", "remove request", "remove request: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			ref := submit(t, h)
			f.calls["RemoveOUTags"] = true
			return svc.Withdraw(ctx, ref, "anna@example.com")
		}},
		{"extend: tag", "tag box", "tag box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["SetTags"] = true
			_, err := svc.Extend(ctx, a.ID, a.ExpiresAt.Add(24*time.Hour), "anna@example.com", false)
			return err
		}},
		{"extend: untag", "untag box", "untag box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["RemoveTags"] = true
			_, err := svc.Extend(ctx, a.ID, a.ExpiresAt.Add(24*time.Hour), "anna@example.com", false)
			return err
		}},
		{"close: tag", "tag box", "tag box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["SetTags"] = true
			_, err := svc.RequestClose(ctx, a.ID, "anna@example.com")
			return err
		}},
		{"close", "close box", "close box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["CloseAccount"] = true
			_, err := svc.RequestClose(ctx, a.ID, "anna@example.com")
			return err
		}},
		{"verify the closure", "verify closure of box", "verify closure of box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["GetAccount"] = true
			_, err := svc.RequestClose(ctx, a.ID, "anna@example.com")
			return err
		}},
		{"pull costs", "costs for box", "costs for box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["Costs"] = true
			_, err := svc.Costs(ctx, a.ID, 30, true)
			return err
		}},
		{"costs from a failed pull", "costs for box", "costs for box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			a := h.active(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["Costs"] = true
			if _, err := svc.Costs(ctx, a.ID, 30, true); err == nil {
				t.Fatal("the pull did not fail")
			}
			// Within the wait after a failure, Costs answers from the cache
			// and calls nothing.
			f.calls["Costs"] = false
			_, err := svc.Costs(ctx, a.ID, 30, false)
			return err
		}},
		{"inspect the budget", "inspect budget before change", "inspect budget before change: " + errAWS.Error(), func(t *testing.T, h *harness, _ *core.Service, _ *failing) error {
			// The budget needs enforcement, so this case has its own service.
			enforceBudgets(h)
			svc, f := failingService(h)
			a := h.ready(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["InspectBudget"] = true
			_, err := svc.SetBudget(ctx, a.ID, 100, "admin", "raise", false)
			return err
		}},
		{"refresh: list accounts", "list playground accounts", "list playground accounts: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			f.calls["ListPlaygroundAccounts"] = true
			_, err := svc.Refresh(ctx)
			return err
		}},
		{"refresh: warn", "tag box", "tag box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			h.ready(t, "box", "anna@example.com", 10*24*time.Hour)
			h.advance(8 * 24 * time.Hour)
			f.calls["SetTags"] = true
			_, err := svc.Refresh(ctx)
			return err
		}},
		// The budget step wraps both failed calls and refusals of core. Its
		// own prefix stays a plain wrap, and the provider call under it is
		// the ProviderError, with no Op of its own.
		{"refresh: budget", "", "budget for box: " + errAWS.Error(), func(t *testing.T, h *harness, svc *core.Service, f *failing) error {
			h.ready(t, "box", "anna@example.com", 10*24*time.Hour)
			f.calls["EnsureBudget"] = true
			_, err := svc.Refresh(ctx)
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			svc, f := failingService(h)
			err := c.run(t, h, svc, f)
			if err == nil {
				t.Fatal("the call did not fail")
			}
			// Refresh joins the errors of its steps, so its text holds the
			// step among the others.
			got := err.Error()
			if joined := strings.HasPrefix(c.name, "refresh: "); joined && !strings.Contains(got, c.want) || !joined && got != c.want {
				t.Errorf("error = %q, want the text %q", got, c.want)
			}
			if ops := providerOps(err); !slices.Contains(ops, c.op) {
				t.Errorf("ProviderError ops = %q, want one with Op %q in %v", ops, c.op, err)
			}
			if !errors.Is(err, errAWS) {
				t.Errorf("errors.Is does not reach the provider error in %v", err)
			}
		})
	}

	// A refusal of core under the same budget prefix is not a ProviderError.
	t.Run("refresh: budget refusal", func(t *testing.T) {
		h := newHarness(t)
		enforceBudgets(h)
		a := h.ready(t, "box", "anna@example.com", 10*24*time.Hour)
		if err := h.prov.SetTags(ctx, a.ID, map[string]string{core.TagBudgetPolicy: "p-other"}); err != nil {
			t.Fatal(err)
		}
		svc, _ := failingService(h)
		_, err := svc.Refresh(ctx)
		if err == nil || !strings.Contains(err.Error(), "budget for box: budget enforcement configuration removed or changed") {
			t.Fatalf("refresh = %v, want the budget refusal", err)
		}
		var pe *core.ProviderError
		if errors.As(err, &pe) {
			t.Errorf("a refusal of core reads as a failed provider call: %+v in %v", pe, err)
		}
	})
}
