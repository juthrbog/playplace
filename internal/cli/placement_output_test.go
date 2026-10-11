package cli

import (
	"strings"
	"testing"

	"playplace/internal/core"
)

// init says when it created the OU, because a new OU has none of the
// operator's SCPs. Otherwise it only names the OU it found.
func TestInitSaysWhenItCreatedTheOU(t *testing.T) {
	for _, tc := range []struct {
		created bool
		want    string
	}{
		{true, "created playground OU ou-new under the root. Attach the safeguards SCP to it before you grant access.\n"},
		{false, "playground OU: ou-new\n"},
	} {
		text, _ := captureStdout(t, func() error {
			printInit(core.OrgInfo{OrgID: "o-1", ManagementAccountID: "222222222222", PlaygroundOUID: "ou-new", OUCreated: tc.created})
			return nil
		})
		if !strings.HasSuffix(text, tc.want) {
			t.Errorf("OUCreated=%v: output = %q, want suffix %q", tc.created, text, tc.want)
		}
	}
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
