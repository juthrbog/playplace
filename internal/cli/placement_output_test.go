package cli

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"playplace/internal/core"
)

// init says when it created the OU, because a new OU has none of the
// operator's SCPs. Otherwise it only names the OU it found. init did not make
// an OU that --ou-id names, so init names it and asks for the safeguards SCP.
func TestInitSaysWhenItCreatedTheOU(t *testing.T) {
	for _, tc := range []struct {
		created, byID bool
		want          string
	}{
		{true, false, "created playground OU ou-new under the root. Attach the safeguards SCP to it before you grant access.\n"},
		{false, false, "playground OU: ou-new\n"},
		{false, true, "playground OU: Lab (ou-new), found by --ou-id. init did not create or change it.\n" +
			"Attach the safeguards SCP to this OU before you grant access.\n"},
	} {
		text, _ := captureStdout(t, func() error {
			printInit(core.OrgInfo{OrgID: "o-1", ManagementAccountID: "222222222222", PlaygroundOUID: "ou-new", PlaygroundOUName: "Lab", OUCreated: tc.created}, tc.byID)
			return nil
		})
		if !strings.HasSuffix(text, tc.want) {
			t.Errorf("OUCreated=%v byID=%v: output = %q, want suffix %q", tc.created, tc.byID, text, tc.want)
		}
	}
}

// A valid --ou-id that names the wrong OU passes every check. init lists the
// active accounts that playplace does not manage, with what the next refresh
// does to each: the owner, the budget and the closure that adoption gives it.
// It leaves out managed and closed accounts, and it changes nothing.
func TestInitListsTheAccountsThatRefreshAdopts(t *testing.T) {
	const tail = "The next refresh does this, and the dates assume that it runs today. Move every account that playplace must not manage out of the OU before you run another command.\n"
	for _, tc := range []struct {
		name string
		seed []core.RemoteAccount
		want string // the output after the OU line; DEFAULT is the default expiry date
		// refresh is what the next reconcile prints, with the names in
		// each part sorted.
		refresh string
		// adopted holds, by account ID, the owner, budget and expiry date
		// that the refresh writes, as "OWNER BUDGET DATE".
		adopted map[string]string
	}{
		{name: "empty OU", refresh: "nothing to do"},
		{name: "managed and closed only", refresh: "nothing to do", seed: []core.RemoteAccount{
			{ProviderID: "111111111111", Name: "dev-mine", Tags: map[string]string{core.TagManaged: "true"}},
			{ProviderID: "222222222222", Name: "old-closed", Status: "CLOSED"},
		}},
		{name: "one", seed: []core.RemoteAccount{
			{ProviderID: "111111111111", Name: "dev-mine", Tags: map[string]string{core.TagManaged: "true"}},
			{ProviderID: "333333333333", Name: "team-dev"},
		}, want: "playplace does not manage 1 account in this OU:\n" +
			"  team-dev (333333333333): adopted with owner unknown and $50/month, closure starts on DEFAULT\n" + tail,
			refresh: "adopted team-dev",
			adopted: map[string]string{"333333333333": "unknown 50 DEFAULT"}},
		{name: "kept tags", seed: []core.RemoteAccount{
			{ProviderID: "444444444444", Name: "team-prod", Tags: map[string]string{core.TagOwner: "ops", core.TagExpires: "2099-01-02T00:00:00Z", core.TagBudget: "75"}},
			{ProviderID: "333333333333", Name: "team-dev"},
		}, want: "playplace does not manage 2 accounts in this OU:\n" +
			"  team-dev (333333333333): adopted with owner unknown and $50/month, closure starts on DEFAULT\n" +
			"  team-prod (444444444444): adopted with owner ops and $75/month, closure starts on 2099-01-02\n" + tail,
			refresh: "adopted team-dev, team-prod",
			adopted: map[string]string{"333333333333": "unknown 50 DEFAULT", "444444444444": "ops 75 2099-01-02"}},
		{name: "expired or closing", seed: []core.RemoteAccount{
			{ProviderID: "555555555555", Name: "old-lab", Tags: map[string]string{core.TagOwner: "ops", core.TagExpires: "2020-01-01T00:00:00Z"}},
			{ProviderID: "666666666666", Name: "quit-lab", Tags: map[string]string{core.TagCloseRequested: "2020-01-01T00:00:00Z"}},
		}, want: "playplace does not manage 2 accounts in this OU:\n" +
			"  old-lab (555555555555): adopted with owner ops and $50/month, closure starts at once\n" +
			"  quit-lab (666666666666): closure starts at once, because it has a playplace:close-requested tag\n" + tail,
			refresh: "adopted old-lab; closed old-lab, quit-lab",
			adopted: map[string]string{"555555555555": "ops 50 2020-01-01"}},
		{name: "damaged tags", seed: []core.RemoteAccount{
			{ProviderID: "777777777777", Name: "bad-lab", Tags: map[string]string{core.TagCloseRequested: "soon"}},
		}, want: "playplace does not manage 1 account in this OU:\n" +
			"  bad-lab (777777777777): left alone until someone repairs its tags: account bad-lab: playplace:close-requested is nonempty but invalid; operator tag repair required\n" + tail,
			refresh: "nothing to do"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			prov := useFake(t)
			for _, r := range tc.seed {
				prov.Seed(r)
			}
			before := time.Now()
			var text string
			captureStderr(t, func() {
				var err error
				text, err = captureStdout(t, func() error {
					cmd := New()
					cmd.SetArgs([]string{"init", "--ou-id", "ou-abcd-12345678", "--history", "none"})
					return cmd.ExecuteContext(context.Background())
				})
				if err != nil {
					t.Error(err)
				}
			})
			after := time.Now()
			_, got, ok := strings.Cut(text, "Attach the safeguards SCP to this OU before you grant access.\n")
			if !ok {
				t.Fatalf("init did not report the OU:\n%s", text)
			}
			// The default expiry is 14 days from now. Accept either day if
			// the run crossed midnight, here or in UTC.
			matches := func(got, want string) bool {
				for _, at := range []time.Time{before, after, before.UTC(), after.UTC()} {
					if got == strings.ReplaceAll(want, "DEFAULT", at.Add(14*24*time.Hour).Format("2006-01-02")) {
						return true
					}
				}
				return false
			}
			if !matches(got, tc.want) {
				t.Fatalf("output after the OU line:\n%s\nwant:\n%s", got, tc.want)
			}
			// init adopts nothing itself.
			for _, r := range tc.seed {
				if got, err := prov.GetAccount(context.Background(), r.ProviderID); err != nil || got.Tags[core.TagManaged] != r.Tags[core.TagManaged] || got.Tags[core.TagHandoff] != "" {
					t.Fatalf("init changed %s: %+v, %v", r.Name, got.Tags, err)
				}
			}
			// The list is a forecast: the next refresh does what it says.
			var sum string
			captureStderr(t, func() {
				sum, _ = captureStdout(t, func() error {
					cmd := New()
					cmd.SetArgs([]string{"reconcile", "--ou-id", "ou-abcd-12345678", "--history", "none"})
					return cmd.ExecuteContext(context.Background())
				})
			})
			if got := sortedSummary(strings.TrimSpace(sum)); got != tc.refresh {
				t.Fatalf("reconcile after init = %q, want %q", got, tc.refresh)
			}
			for id, want := range tc.adopted {
				acct, err := prov.GetAccount(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				expires, _, _ := strings.Cut(acct.Tags[core.TagExpires], "T")
				if got := acct.Tags[core.TagOwner] + " " + acct.Tags[core.TagBudget] + " " + expires; !matches(got, want) {
					t.Errorf("refresh wrote %s as %q, but init said %q", acct.Name, got, want)
				}
			}
		})
	}
}

// sortedSummary sorts the names in each part of a refresh summary, such as
// "adopted b, a; closed c", because the fake lists accounts in map order.
func sortedSummary(sum string) string {
	parts := strings.Split(sum, "; ")
	for i, part := range parts {
		// A label can have two words ("granted access"), and an account
		// name has none.
		list := strings.Split(part, ", ")
		cut := strings.LastIndex(list[0], " ")
		if cut < 0 || part == "nothing to do" {
			continue
		}
		label := list[0][:cut]
		list[0] = list[0][cut+1:]
		sort.Strings(list)
		parts[i] = label + " " + strings.Join(list, ", ")
	}
	return strings.Join(parts, "; ")
}

// list prints the note of a creation that waits outside the OU, next to
// the table, so the row is not just "creating".
func TestListPrintsTheNoteOfAnUnplacedCreation(t *testing.T) {
	text, _ := captureStdout(t, func() error {
		printAccounts([]*core.Account{
			{Name: "waiting", Owner: "owner", Status: core.StatusCreating, ProviderID: "333333333333", LastError: "It waits outside the Playground OU for the next reconcile."},
			{Name: "fresh", Owner: "owner", Status: core.StatusCreating},
		})
		return nil
	})
	if !strings.Contains(text, "\nwaiting: It waits outside the Playground OU for the next reconcile.\n") {
		t.Fatalf("output = %q", text)
	}
	if strings.Contains(text, "fresh:") {
		t.Fatalf("a creation without a note printed one: %q", text)
	}
}
