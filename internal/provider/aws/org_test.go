package aws

import (
	"context"
	"slices"
	"strings"
	"testing"

	"playplace/internal/core"
)

// orgAPI models an organization at the AWS API: whether it exists, which OUs
// sit directly under the root, and each account's parent. Accounts are
// named "a-<id>" and CREATED unless names or invited say otherwise.
type orgAPI struct {
	t        *testing.T
	inUse    bool
	rootOUs  map[string]string // name -> id
	parents  map[string]string // account id -> parent id
	names    map[string]string // account id -> name
	invited  map[string]bool   // account id -> joined by invitation
	calls    []string
	tagReads []string // account ids whose tags were read
}

func (o *orgAPI) respond(name string, in map[string]any) (any, int) {
	o.calls = append(o.calls, name)
	switch name {
	case "DescribeOrganization":
		if !o.inUse {
			return map[string]any{"__type": "AWSOrganizationsNotInUseException", "Message": "no organization"}, 400
		}
		return map[string]any{"Organization": map[string]any{"Id": "o-1", "MasterAccountId": "222222222222"}}, 200
	case "CreateOrganization":
		o.inUse = true
		return map[string]any{"Organization": map[string]any{"Id": "o-1", "MasterAccountId": "222222222222"}}, 200
	case "ListRoots":
		return map[string]any{"Roots": []map[string]any{{"Id": "r-root"}}}, 200
	case "ListOrganizationalUnitsForParent":
		var ous []map[string]any
		for name, id := range o.rootOUs {
			ous = append(ous, map[string]any{"Id": id, "Name": name})
		}
		return map[string]any{"OrganizationalUnits": ous}, 200
	case "CreateOrganizationalUnit":
		o.rootOUs[in["Name"].(string)] = "ou-new"
		return map[string]any{"OrganizationalUnit": map[string]any{"Id": "ou-new", "Name": in["Name"]}}, 200
	case "ListAccountsForParent":
		var accounts []map[string]any
		for id, parent := range o.parents {
			if parent == in["ParentId"] {
				name, joined := "a-"+id, "CREATED"
				if n, ok := o.names[id]; ok {
					name = n
				}
				if o.invited[id] {
					joined = "INVITED"
				}
				accounts = append(accounts, map[string]any{"Id": id, "Name": name, "State": "ACTIVE", "JoinedMethod": joined})
			}
		}
		return map[string]any{"Accounts": accounts}, 200
	case "ListTagsForResource":
		o.tagReads = append(o.tagReads, in["ResourceId"].(string))
		return map[string]any{"Tags": []map[string]string{{"Key": core.TagManaged, "Value": "true"}}}, 200
	case "ListParents":
		return map[string]any{"Parents": []map[string]any{{"Id": o.parents[in["ChildId"].(string)]}}}, 200
	case "MoveAccount":
		if in["SourceParentId"] != o.parents[in["AccountId"].(string)] {
			return map[string]any{"__type": "SourceParentNotFoundException", "Message": "wrong source"}, 400
		}
		o.parents[in["AccountId"].(string)] = in["DestinationParentId"].(string)
		return map[string]any{}, 200
	case "TagResource":
		return map[string]any{}, 200
	}
	return unexpectedBudgetAPI(o.t, name)
}

func orgProvider(t *testing.T, o *orgAPI, ouName string) *Provider {
	p := budgetHTTP(t, o.respond)
	p.info = nil // resolve against the API, not the budget tests' fixture
	p.cfg.PlaygroundOUName = ouName
	return p
}

// Only `playplace init` may create the organization or the OU. A wrong
// profile, a typo, or a nested OU must fail every other call closed.
func TestOnlyInitCreatesTheOrganizationAndOU(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, ouName string
		inUse        bool
		want         string
	}{
		{"standalone account", "Playground", false, "not in an AWS Organization"},
		{"typo", "Playgroud", true, `playground OU "Playgroud" is not a direct child`},
		{"nested OU", "Playground", true, `playground OU "Playground" is not a direct child`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &orgAPI{t: t, inUse: tc.inUse, rootOUs: map[string]string{"Sandbox": "ou-sandbox"}, parents: map[string]string{}}
			if tc.name == "typo" {
				o.rootOUs["Playground"] = "ou-real"
			}
			p := orgProvider(t, o, tc.ouName)
			calls := map[string]func() error{
				"ListPlaygroundAccounts": func() error { _, err := p.ListPlaygroundAccounts(ctx); return err },
				"ListRootAccounts":       func() error { _, err := p.ListRootAccounts(ctx); return err },
				"OUTags":                 func() error { _, err := p.OUTags(ctx); return err },
				"SetOUTags":              func() error { return p.SetOUTags(ctx, map[string]string{"k": "v"}) },
				"PlaceAccount":           func() error { return p.PlaceAccount(ctx, "333333333333", nil) },
				"EnsureBudget":           func() error { _, err := p.EnsureBudget(ctx, "333333333333", budgetSpec()); return err },
			}
			for name, call := range calls {
				// init is named only with its condition: for a nested OU or
				// a wrong profile, running it would build a second, empty OU
				// or a new organization.
				if err := call(); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "`playplace init` only if") {
					t.Errorf("%s: %v", name, err)
				}
			}
			if slices.Contains(o.calls, "CreateOrganization") || slices.Contains(o.calls, "CreateOrganizationalUnit") {
				t.Fatalf("a runtime call created organization resources: %v", o.calls)
			}

			// init creates what it is asked for and says so, once.
			info, err := p.EnsureOrganization(ctx)
			if err != nil || info.PlaygroundOUID != "ou-new" || info.Created == tc.inUse || !info.OUCreated {
				t.Fatalf("init: %+v %v", info, err)
			}
			if again, err := p.EnsureOrganization(ctx); err != nil || again.Created || again.OUCreated {
				t.Fatalf("second init: %+v %v", again, err)
			}
			if _, err := p.ListPlaygroundAccounts(ctx); err != nil {
				t.Fatalf("after init: %v", err)
			}
		})
	}
}

func TestPlaceAccountMovesOnlyOutOfTheRoot(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		parent string
		moved  bool
		err    string
	}{
		{"r-root", true, ""},
		{"ou-play", false, ""},
		{"ou-quarantine", false, "not the organization root"},
	} {
		t.Run(tc.parent, func(t *testing.T) {
			o := &orgAPI{t: t, inUse: true, rootOUs: map[string]string{"Playground": "ou-play"}, parents: map[string]string{"333333333333": tc.parent}}
			p := orgProvider(t, o, "Playground")
			err := p.PlaceAccount(ctx, "333333333333", map[string]string{core.TagHandoff: "placing"})
			if (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
				t.Fatalf("err = %v", err)
			}
			if slices.Contains(o.calls, "MoveAccount") != tc.moved || slices.Contains(o.calls, "TagResource") != (tc.err == "") {
				t.Fatalf("calls = %v", o.calls)
			}
			if want := map[bool]string{true: "ou-play", false: tc.parent}[tc.moved]; o.parents["333333333333"] != want {
				t.Fatalf("parent = %s, want %s", o.parents["333333333333"], want)
			}
		})
	}
}

// Every inventory read lists the root. Accounts that cannot be playplace
// creations must cost no tag read: the management account, invited accounts,
// and names that playplace never gives.
func TestListRootAccountsReadsTagsOnlyForPossibleCreations(t *testing.T) {
	o := &orgAPI{t: t, inUse: true, rootOUs: map[string]string{"Playground": "ou-play"},
		parents: map[string]string{
			"222222222222": "r-root", // management account
			"333333333333": "r-root", // a possible creation
			"444444444444": "ou-play",
			"555555555555": "r-root", // invited
			"666666666666": "r-root", // a name playplace never gives
		},
		names:   map[string]string{"666666666666": "Production Billing"},
		invited: map[string]bool{"555555555555": true},
	}
	p := orgProvider(t, o, "Playground")
	accounts, err := p.ListRootAccounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].ProviderID != "333333333333" || accounts[0].Tags[core.TagManaged] != "true" {
		t.Fatalf("root accounts = %+v %v", accounts, err)
	}
	if !slices.Equal(o.tagReads, []string{"333333333333"}) {
		t.Fatalf("tag reads = %v, want only the possible creation", o.tagReads)
	}
}

// A failed lookup is not cached. A running serve recovers once `playplace
// init` runs in another process, without a restart and without creating
// anything itself.
func TestFailedOrganizationLookupIsNotCached(t *testing.T) {
	ctx := context.Background()
	o := &orgAPI{t: t, inUse: true, rootOUs: map[string]string{}, parents: map[string]string{"333333333333": "ou-x"}}
	p := orgProvider(t, o, "Playground")
	if _, err := p.ListPlaygroundAccounts(ctx); err == nil {
		t.Fatal("missing OU was not reported")
	}
	o.rootOUs["Playground"] = "ou-x" // init ran elsewhere
	if accounts, err := p.ListPlaygroundAccounts(ctx); err != nil || len(accounts) != 1 {
		t.Fatalf("lookup did not recover after init: %+v %v", accounts, err)
	}
	if slices.Contains(o.calls, "CreateOrganization") || slices.Contains(o.calls, "CreateOrganizationalUnit") {
		t.Fatalf("a runtime call created organization resources: %v", o.calls)
	}
}
