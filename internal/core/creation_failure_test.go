package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

const failedSubject = "Playground account creation failed"

// queueWrites fails the next fail writes of the request queue.
type queueWrites struct {
	core.Provider
	fail int
}

func (p *queueWrites) SetOUTags(ctx context.Context, tags map[string]string) error {
	if p.fail > 0 {
		p.fail--
		return errors.New("queue unavailable")
	}
	return p.Provider.SetOUTags(ctx, tags)
}

// failureWitness keeps the failed events and failure notices, and whether the
// record of name carried the failure flag when each one was sent.
type failureWitness struct {
	prov    *fake.Provider
	name    string
	events  []core.AuditEvent
	notices []core.Message
	flagged []bool
}

func (w *failureWitness) record() (core.Request, bool) {
	tags, _ := w.prov.OUTags(context.Background())
	v, ok := tags[core.RequestTagPrefix+w.name]
	if !ok {
		return core.Request{}, false
	}
	r, err := core.DecodeRequest(core.RequestTagPrefix+w.name, v)
	return r, err == nil
}

func (w *failureWitness) Record(_ context.Context, e core.AuditEvent) error {
	if e.Event == "failed" {
		r, _ := w.record()
		w.events, w.flagged = append(w.events, e), append(w.flagged, r.FailureReported)
	}
	return nil
}

func (w *failureWitness) Notify(_ context.Context, m core.Message) error {
	if m.Subject == failedSubject {
		r, _ := w.record()
		w.notices, w.flagged = append(w.notices, m), append(w.flagged, r.FailureReported)
	}
	return nil
}

// failedApproval approves a request whose creation AWS fails, as the web,
// Slack and TUI do: nobody waits for the creation.
func failedApproval(t *testing.T) (*harness, *core.Service, *queueWrites, *failureWitness, core.Request, *core.Account) {
	t.Helper()
	h := newHarness(t)
	ctx := context.Background()
	p := &queueWrites{Provider: h.prov}
	w := &failureWitness{prov: h.prov, name: "failure"}
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(p, w, cfg, nil)
	svc.Now = h.svc.Now
	svc.SetAuditor(w)
	r, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "failure", Owner: "alice@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	h.prov.FailNext = "EMAIL_ALREADY_EXISTS"
	a, err := svc.Approve(ctx, r.Ref(), "lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return h, svc, p, w, r, a
}

func checkFailureReport(t *testing.T, w *failureWitness, r core.Request, a *core.Account) {
	t.Helper()
	if len(w.events) != 1 || len(w.notices) != 1 {
		t.Fatalf("reports: events=%+v notices=%+v", w.events, w.notices)
	}
	e, m := w.events[0], w.notices[0]
	if e.JourneyID != r.JourneyID || e.Owner != r.Owner || e.Details["reason"] != "EMAIL_ALREADY_EXISTS" || e.Details["request_id"] != a.RequestID {
		t.Fatalf("failed event=%+v", e)
	}
	// The body names the AWS reason and the next step for it.
	if m.Owner != r.Owner || m.AccountID != a.RequestID || !strings.Contains(m.Body, "EMAIL_ALREADY_EXISTS") || !strings.Contains(m.Body, "with another name") {
		t.Fatalf("failure notice=%+v", m)
	}
	for _, flagged := range w.flagged {
		if !flagged {
			t.Fatalf("reported before the flag was written: %v", w.flagged)
		}
	}
}

func TestFailedCreationIsReportedOnce(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		first func(svc *core.Service, a *core.Account) error
	}{
		{"by refresh with nobody waiting", func(svc *core.Service, _ *core.Account) error {
			_, err := svc.Refresh(ctx)
			return err
		}},
		{"by a wait", func(svc *core.Service, a *core.Account) error {
			_, err := svc.PollCreate(ctx, a.RequestID, a)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc, _, w, r, a := failedApproval(t)
			if err := tc.first(svc, a); err != nil {
				t.Fatal(err)
			}
			checkFailureReport(t, w, r, a)
			// A flagged record is never reported again, by either path.
			if _, err := svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := svc.PollCreate(ctx, a.RequestID, a)
			if err != nil || got.Status != core.StatusFailed || got.HistoryID() != r.JourneyID {
				t.Fatalf("poll after report=%+v %v", got, err)
			}
			checkFailureReport(t, w, r, a)
		})
	}
}

func TestFailedCreationIsNotReportedWithoutItsFlag(t *testing.T) {
	ctx := context.Background()
	_, svc, p, w, r, a := failedApproval(t)
	p.fail = 1
	if _, err := svc.Refresh(ctx); err == nil {
		t.Fatal("refresh hid the failed flag write")
	}
	if rec, ok := w.record(); len(w.events)+len(w.notices) != 0 || !ok || rec.FailureReported {
		t.Fatalf("reported without the flag: %+v %+v %+v", w.events, w.notices, rec)
	}
	p.fail = 1
	if _, err := svc.PollCreate(ctx, a.RequestID, a); err == nil || len(w.events)+len(w.notices) != 0 {
		t.Fatalf("poll reported without the flag: %v %+v %+v", err, w.events, w.notices)
	}
	if _, err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	checkFailureReport(t, w, r, a)
}

func TestFailedCreationIsReportedBeforeItsRecordIsRemoved(t *testing.T) {
	ctx := context.Background()
	h, svc, _, w, r, a := failedApproval(t)
	h.advance(svc.Config().FailedRetention + time.Hour)
	if _, err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	checkFailureReport(t, w, r, a)
	if _, ok := w.record(); ok {
		t.Fatal("record kept after FailedRetention")
	}
}

func TestDirectCreationFailureKeepsItsJourney(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	rec := &recorderAudit{}
	h.svc.SetAuditor(rec)
	h.prov.FailNext = "EMAIL_ALREADY_EXISTS"
	a, err := h.svc.Request(ctx, core.RequestInput{Name: "direct", Owner: "alice@example.com", RequestedBy: "ops@example.com", ApprovedBy: "ops@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.PollCreate(ctx, a.RequestID, a)
	if err != nil || got.Status != core.StatusFailed || got.HistoryID() != a.JourneyID {
		t.Fatalf("direct failure=%+v %v", got, err)
	}
	e := rec.events[len(rec.events)-1]
	if e.Event != "failed" || e.JourneyID != a.JourneyID || e.Owner != "alice@example.com" || e.Details["request_id"] != a.RequestID {
		t.Fatalf("failed event=%+v", e)
	}
	if len(h.note.msgs) != 1 || h.note.msgs[0].Subject != failedSubject || h.note.msgs[0].Owner != "alice@example.com" {
		t.Fatalf("notices=%+v", h.note.msgs)
	}
}

func TestResubmissionReportsTheFailureItReplaces(t *testing.T) {
	ctx := context.Background()
	_, svc, _, w, r, a := failedApproval(t)
	again, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "failure", Owner: "alice@example.com"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	checkFailureReport(t, w, r, a)
	queued, ok := w.record()
	if !ok || queued.Approved || queued.FailureReported || queued.JourneyID != again.JourneyID || again.JourneyID == r.JourneyID {
		t.Fatalf("queued=%+v %v", queued, ok)
	}
	if _, err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	checkFailureReport(t, w, r, a)
}

// hookList runs hook once, inside the next ListCreateRequests call. It plays
// a second process that acts between a pass's queue read and its write.
type hookList struct {
	core.Provider
	hook func()
}

func (p *hookList) ListCreateRequests(ctx context.Context) ([]core.CreateRequest, error) {
	if h := p.hook; h != nil {
		p.hook = nil
		h()
	}
	return p.Provider.ListCreateRequests(ctx)
}

func TestFailedCreationRereadsTheRecordOfAnotherProcess(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		// other acts in process b; it returns the journey the record must hold.
		other func(t *testing.T, b *core.Service, r core.Request) string
	}{
		{"that reported it", func(t *testing.T, b *core.Service, r core.Request) string {
			if _, err := b.Refresh(ctx); err != nil {
				t.Error(err)
			}
			return r.JourneyID
		}},
		{"that queued a new request", func(t *testing.T, b *core.Service, _ core.Request) string {
			again, err := b.SubmitRequest(ctx, core.RequestInput{Name: "failure", Owner: "alice@example.com"}, "web")
			if err != nil {
				t.Error(err)
			}
			return again.JourneyID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _, w, r, a := failedApproval(t)
			p := &hookList{Provider: h.prov}
			svc := core.NewService(p, w, b.Config(), nil)
			svc.Now = b.Now
			svc.SetAuditor(w)
			var want string
			p.hook = func() { want = tc.other(t, b, r) }
			if _, err := svc.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			checkFailureReport(t, w, r, a)
			if rec, ok := w.record(); !ok || rec.JourneyID != want {
				t.Fatalf("record=%+v %v, want journey %s", rec, ok, want)
			}
		})
	}
}

func TestFailedCreationOfAFullRecordIsReported(t *testing.T) {
	ctx := context.Background()
	owner := "alice.with.a.rather.long.name@example.com"
	approver := "lead.with.a.rather.long.name@example.com"
	approved := func(purpose string) (*harness, *core.Service, *failureWitness, core.Request) {
		h := newHarness(t)
		w := &failureWitness{prov: h.prov, name: "failure"}
		svc := core.NewService(h.prov, w, h.svc.Config(), nil)
		svc.Now = h.svc.Now
		svc.SetAuditor(w)
		r, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "failure", Owner: owner, Purpose: purpose}, "web")
		if err != nil {
			t.Fatal(err)
		}
		h.prov.FailNext = "EMAIL_ALREADY_EXISTS"
		if _, err := svc.Approve(ctx, r.Ref(), approver); err != nil {
			t.Fatal(err)
		}
		rec, ok := w.record()
		if !ok || rec.CreateRequestID == "" {
			t.Fatalf("approved record=%+v %v", rec, ok)
		}
		return h, svc, w, rec
	}
	// Find the purpose that makes the approved record exactly MaxTagValue
	// characters long, so the flag does not fit next to it.
	_, _, _, rec := approved("")
	purpose := ""
	for n := 1; n <= core.MaxPurpose; n++ {
		rec.Purpose = strings.Repeat("p", n)
		if utf8.RuneCountInString(rec.Encode()) == core.MaxTagValue {
			purpose = rec.Purpose
			break
		}
	}
	if purpose == "" {
		t.Fatal("no purpose fills the record")
	}
	_, svc, w, _ := approved(purpose)
	if _, err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok := w.record()
	if len(w.events) != 1 || len(w.notices) != 1 || !ok || !got.FailureReported {
		t.Fatalf("reports: events=%d notices=%d record=%+v", len(w.events), len(w.notices), got)
	}
}
