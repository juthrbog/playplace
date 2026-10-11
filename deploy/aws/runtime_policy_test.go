package aws_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// bootstrapActions are what only `playplace init` needs: creating the
// organization (with its service-linked role) and the Playground OU.
var bootstrapActions = []string{"organizations:CreateOrganization", "organizations:CreateOrganizationalUnit", "iam:CreateServiceLinkedRole"}

// allowedActions returns the action patterns of every Allow statement. An
// Allow with NotAction allows almost everything, so it fails the test.
func allowedActions(t *testing.T, file string) []string {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("."), file) // only files in this directory
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Statement []struct {
			Effect    string
			Action    json.RawMessage
			NotAction json.RawMessage
		}
	}
	if err := json.Unmarshal(b, &policy); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	var out []string
	for _, s := range policy.Statement {
		if s.Effect != "Allow" {
			continue
		}
		if len(s.NotAction) > 0 {
			t.Fatalf("%s: Allow with NotAction", file)
		}
		var actions []string
		if err := json.Unmarshal(s.Action, &actions); err != nil {
			var one string
			if err := json.Unmarshal(s.Action, &one); err != nil {
				t.Fatalf("%s: action %s", file, s.Action)
			}
			actions = []string{one}
		}
		out = append(out, actions...)
	}
	return out
}

// actionMatches reports whether an IAM action pattern covers action. IAM
// compares actions without regard to case, and * and ? are wildcards.
// Actions have no "/", so path.Match has the same wildcard rules.
func actionMatches(t *testing.T, pattern, action string) bool {
	t.Helper()
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(action))
	if err != nil {
		t.Fatalf("pattern %q: %v", pattern, err)
	}
	return ok
}

// The runtime role must not be able to create an organization or an OU, so
// a wrong profile or OU name fails instead of building an unguarded OU.
// Only the separate bootstrap policy, used for `playplace init`, may.
func TestOnlyTheBootstrapPolicyCreatesOrganizationResources(t *testing.T) {
	runtime := allowedActions(t, "playplace-policy.json")
	bootstrap := allowedActions(t, "playplace-bootstrap-policy.json")
	for _, action := range bootstrapActions {
		for _, pattern := range runtime {
			if actionMatches(t, pattern, action) {
				t.Errorf("playplace-policy.json: %s allows %s", pattern, action)
			}
		}
		allowed := false
		for _, pattern := range bootstrap {
			allowed = allowed || actionMatches(t, pattern, action)
		}
		if !allowed {
			t.Errorf("playplace-bootstrap-policy.json does not allow %s", action)
		}
	}
}

// orgsCall matches an Organizations call in the provider source: a client
// method such as p.orgs.ListParents( or a paginator constructor.
var orgsCall = regexp.MustCompile(`p\.orgs\.(\w+)\(|organizations\.New(\w+)Paginator\(`)

// The runtime policy must allow every Organizations call in aws.go, which
// holds the organization lookup and the account lifecycle. Only the
// bootstrap calls of `playplace init` are left to the bootstrap policy. The
// calls come from the source, so a new call, such as the OU lookup by
// --ou-id, cannot ship without its permission.
func TestRuntimePolicyAllowsTheLifecycleOrganizationsCalls(t *testing.T) {
	src, err := fs.ReadFile(os.DirFS("../.."), "internal/provider/aws/aws.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]bool{}
	for _, m := range orgsCall.FindAllStringSubmatch(string(src), -1) {
		calls["organizations:"+m[1]+m[2]] = true
	}
	// The scan must see a client call and a paginator, or it proves nothing.
	if !calls["organizations:DescribeOrganizationalUnit"] || !calls["organizations:ListAccountsForParent"] {
		t.Fatalf("the source scan missed known calls: %v", calls)
	}
	runtime := allowedActions(t, "playplace-policy.json")
	for call := range calls {
		if slices.Contains(bootstrapActions, call) {
			continue
		}
		allowed := false
		for _, pattern := range runtime {
			allowed = allowed || actionMatches(t, pattern, call)
		}
		if !allowed {
			t.Errorf("playplace-policy.json does not allow %s", call)
		}
	}
}

func TestActionMatchingFollowsIAMWildcards(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    bool
	}{
		{"*", true},
		{"organizations:*", true},
		{"organizations:C*", true},
		{"Organizations:createorganization", true},
		{"organizations:CreateOrganizationalUni?", false},
		{"organizations:CreateOrganization?", false},
		{"organizations:Describe*", false},
		{"iam:*", false},
	} {
		if got := actionMatches(t, tc.pattern, "organizations:CreateOrganization"); got != tc.want {
			t.Errorf("%s matches CreateOrganization = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}
