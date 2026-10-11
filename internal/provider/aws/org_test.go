package aws

import (
	"context"
	"slices"
	"strings"
	"testing"

	"playplace/internal/core"
)

// orgAPI models an organization at the AWS API: whether it exists, which OUs
// sit directly under the root, which sit deeper, and each account's parent.
// Accounts are named "a-<id>" and CREATED unless names or invited say
// otherwise.
type orgAPI struct {
	t        *testing.T
	inUse    bool
	rootOUs  map[string]string // name -> id
	deepOUs  map[string]string // id -> name, for OUs nested in another OU
	ouFields map[string]any    // replaces fields of a described OU. A nil value drops the field
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
	case "DescribeOrganizationalUnit":
		id := in["OrganizationalUnitId"].(string)
		name, ok := o.deepOUs[id]
		for n, rootOU := range o.rootOUs {
			if rootOU == id {
				name, ok = n, true
			}
		}
		if !ok {
			return map[string]any{"__type": "OrganizationalUnitNotFoundException", "Message": "no such OU"}, 400
		}
		ou := map[string]any{"Id": id, "Name": name, "Arn": "arn:aws:organizations::222222222222:ou/o-1/" + id}
		for k, v := range o.ouFields {
			ou[k] = v
			if v == nil {
				delete(ou, k)
			}
		}
		return map[string]any{"OrganizationalUnit": ou}, 200
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

// ouIDProvider is orgProvider with --ou-id set. --ou-name keeps its default,
// and the lookup must not use it.
func ouIDProvider(t *testing.T, o *orgAPI, id string) *Provider {
	p := orgProvider(t, o, "Playground")
	p.cfg.PlaygroundOUID = id
	return p
}

// noCreations fails when a call created organization resources.
func noCreations(t *testing.T, o *orgAPI) {
	t.Helper()
	if slices.Contains(o.calls, "CreateOrganization") || slices.Contains(o.calls, "CreateOrganizationalUnit") {
		t.Fatalf("a call created organization resources: %v", o.calls)
	}
}

// Only `playplace init` may create the organization or the OU. A wrong
// profile, a typo, or a nested OU must fail every other call closed.
func TestOnlyInitCreatesTheOrganizationAndOU(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, ouName string
		inUse        bool
		want         []string
	}{
		{"standalone account", "Playground", false, []string{"not in an AWS Organization"}},
		// A name that is not found can belong to a nested OU, which only
		// --ou-id finds.
		{"typo", "Playgroud", true, []string{`playground OU "Playgroud" is not a direct child`, "set --ou-id (PLAYPLACE_OU_ID)"}},
		{"nested OU", "Playground", true, []string{`playground OU "Playground" is not a direct child`, "set --ou-id (PLAYPLACE_OU_ID)"}},
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
				err := call()
				for _, want := range append(tc.want, "`playplace init` only if") {
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Errorf("%s: %v, want %q", name, err, want)
					}
				}
			}
			noCreations(t, o)

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

// Placement moves an account only out of the root, into the Playground OU,
// at any depth. For a nested OU, the parent OU is not the root, so an
// account there stays where it is.
func TestPlaceAccountMovesOnlyOutOfTheRoot(t *testing.T) {
	ctx := context.Background()
	for _, layout := range []struct {
		name, ouID string // ouID is --ou-id. Empty finds the OU by --ou-name
	}{
		{"OU under the root", ""},
		{"nested OU", "ou-root-playgrnd"},
	} {
		for _, tc := range []struct {
			parent string
			moved  bool
			err    string
		}{
			{"r-root", true, ""},
			{"ou-root-playgrnd", false, ""},
			{"ou-root-sandbox1", false, "not the organization root"}, // the parent of the nested OU
			{"ou-quarantine", false, "not the organization root"},
		} {
			t.Run(layout.name+"/"+tc.parent, func(t *testing.T) {
				o := &orgAPI{t: t, inUse: true, rootOUs: map[string]string{"Sandbox": "ou-root-sandbox1"}, parents: map[string]string{"333333333333": tc.parent}}
				if layout.ouID == "" {
					o.rootOUs["Playground"] = "ou-root-playgrnd"
				} else {
					o.deepOUs = map[string]string{layout.ouID: "Playground"}
				}
				p := ouIDProvider(t, o, layout.ouID)
				err := p.PlaceAccount(ctx, "333333333333", map[string]string{core.TagHandoff: "placing"})
				if (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
					t.Fatalf("err = %v", err)
				}
				if slices.Contains(o.calls, "MoveAccount") != tc.moved || slices.Contains(o.calls, "TagResource") != (tc.err == "") {
					t.Fatalf("calls = %v", o.calls)
				}
				if want := map[bool]string{true: "ou-root-playgrnd", false: tc.parent}[tc.moved]; o.parents["333333333333"] != want {
					t.Fatalf("parent = %s, want %s", o.parents["333333333333"], want)
				}
			})
		}
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
// init` runs in another process, or once AWS finds the OU that --ou-id
// names, without a restart and without creating anything itself.
func TestFailedOrganizationLookupIsNotCached(t *testing.T) {
	ctx := context.Background()
	for _, ouID := range []string{"", "ou-root-playgrnd"} {
		t.Run("ou-id="+ouID, func(t *testing.T) {
			o := &orgAPI{t: t, inUse: true, rootOUs: map[string]string{}, deepOUs: map[string]string{}, parents: map[string]string{"333333333333": "ou-root-playgrnd"}}
			p := ouIDProvider(t, o, ouID) // an empty ouID finds the OU by --ou-name
			if _, err := p.ListPlaygroundAccounts(ctx); err == nil {
				t.Fatal("missing OU was not reported")
			}
			if ouID == "" {
				o.rootOUs["Playground"] = "ou-root-playgrnd" // init ran elsewhere
			} else {
				o.deepOUs[ouID] = "Lab"
			}
			if accounts, err := p.ListPlaygroundAccounts(ctx); err != nil || len(accounts) != 1 {
				t.Fatalf("lookup did not recover: %+v %v", accounts, err)
			}
			noCreations(t, o)
		})
	}
}

// --ou-id finds an OU at any depth with one read-only call, and the lookup
// does not use --ou-name. An OU with the --ou-name name sits under the root
// as a decoy: a lookup by name would list its account instead.
func TestOUIDFindsANestedOU(t *testing.T) {
	ctx := context.Background()
	o := &orgAPI{t: t, inUse: true,
		rootOUs: map[string]string{"Sandbox": "ou-root-sandbox1", "Playground": "ou-root-decoy123"},
		deepOUs: map[string]string{"ou-root-playgrnd": "Lab"},
		parents: map[string]string{
			"222222222222": "r-root", // management account
			"333333333333": "r-root", // a creation that waits for placement
			"444444444444": "ou-root-playgrnd",
			"555555555555": "ou-root-decoy123",
		},
	}
	p := ouIDProvider(t, o, "ou-root-playgrnd")
	accounts, err := p.ListPlaygroundAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ProviderID != "444444444444" {
		t.Fatalf("playground accounts = %+v %v", accounts, err)
	}
	root, err := p.ListRootAccounts(ctx)
	if err != nil || len(root) != 1 || root[0].ProviderID != "333333333333" {
		t.Fatalf("root accounts = %+v %v", root, err)
	}
	if _, err := p.OUTags(ctx); err != nil || !slices.Contains(o.tagReads, "ou-root-playgrnd") {
		t.Fatalf("OU tags: %v, tag reads %v", err, o.tagReads)
	}
	if slices.Contains(o.calls, "ListOrganizationalUnitsForParent") {
		t.Fatalf("the lookup searched by --ou-name: %v", o.calls)
	}
	described := 0
	for _, call := range o.calls {
		if call == "DescribeOrganizationalUnit" {
			described++
		}
	}
	if described != 1 {
		t.Fatalf("described the OU %d times, want once and then the cached result", described)
	}
	noCreations(t, o)
}

// init with --ou-id creates nothing. It reports the OU it found, with the
// name that AWS gives it. No OU with the --ou-name name sits under the root,
// so init by name would create one here.
func TestInitWithOUIDCreatesNothing(t *testing.T) {
	o := &orgAPI{t: t, inUse: true, rootOUs: map[string]string{"Sandbox": "ou-root-sandbox1"}, deepOUs: map[string]string{"ou-root-playgrnd": "Lab"}, parents: map[string]string{}}
	p := ouIDProvider(t, o, "ou-root-playgrnd")
	info, err := p.EnsureOrganization(context.Background())
	want := core.OrgInfo{OrgID: "o-1", ManagementAccountID: "222222222222", RootID: "r-root", PlaygroundOUID: "ou-root-playgrnd", PlaygroundOUName: "Lab"}
	if err != nil || info != want {
		t.Fatalf("init = %+v %v, want %+v", info, err, want)
	}
	if slices.Contains(o.calls, "ListOrganizationalUnitsForParent") {
		t.Fatalf("init searched by --ou-name: %v", o.calls)
	}
	noCreations(t, o)
}

// An --ou-id that the organization does not hold fails every call closed,
// and init creates nothing for it: no OU, and no organization, which cannot
// hold the OU. AWS reports an OU of another organization as not found, so a
// wrong profile ends here too. The ARN check is a second guard. It also
// refuses an OU whose ARN or ID does not tell its organization. init cannot
// fix any of these, so the errors do not offer it.
func TestUnknownOUIDFailsClosed(t *testing.T) {
	ctx := context.Background()
	const unclear = "so playplace cannot tell which organization holds it"
	for _, tc := range []struct {
		name  string
		inUse bool
		// described replaces fields of the OU that AWS describes. Nil means
		// that AWS does not find the OU.
		described map[string]any
		want      string
	}{
		{"unknown id", true, nil, "playground OU ou-root-playgrnd (--ou-id) was not found in organization o-1"},
		{"OU of another organization", true, map[string]any{"Arn": "arn:aws:organizations::111111111111:ou/o-other/ou-root-playgrnd"}, "is in organization o-other, not in o-1"},
		{"no ARN", true, map[string]any{"Arn": nil}, unclear},
		{"ARN that does not parse", true, map[string]any{"Arn": "ou/o-1/ou-root-playgrnd"}, unclear},
		{"ARN of the root", true, map[string]any{"Arn": "arn:aws:organizations::222222222222:root/o-1/r-root"}, unclear},
		{"ARN of another OU", true, map[string]any{"Arn": "arn:aws:organizations::222222222222:ou/o-1/ou-root-sandbox1"}, unclear},
		{"another ID", true, map[string]any{"Id": "ou-root-sandbox1"}, unclear},
		{"no ID", true, map[string]any{"Id": nil}, unclear},
		{"standalone account", false, nil, "not in an AWS Organization"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The OU with the --ou-name name is a decoy: a lookup by name
			// would succeed.
			o := &orgAPI{t: t, inUse: tc.inUse, rootOUs: map[string]string{"Playground": "ou-root-decoy123"}, deepOUs: map[string]string{}, ouFields: tc.described, parents: map[string]string{"333333333333": "r-root"}}
			if tc.described != nil {
				o.deepOUs["ou-root-playgrnd"] = "Lab"
			}
			p := ouIDProvider(t, o, "ou-root-playgrnd")
			calls := map[string]func() error{
				"init":                   func() error { _, err := p.EnsureOrganization(ctx); return err },
				"ListPlaygroundAccounts": func() error { _, err := p.ListPlaygroundAccounts(ctx); return err },
				"ListRootAccounts":       func() error { _, err := p.ListRootAccounts(ctx); return err },
				"OUTags":                 func() error { _, err := p.OUTags(ctx); return err },
				"PlaceAccount":           func() error { return p.PlaceAccount(ctx, "333333333333", nil) },
			}
			for name, call := range calls {
				err := call()
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "--ou-id") || strings.Contains(err.Error(), "Run `playplace init`") {
					t.Errorf("%s: %v, want %q", name, err, tc.want)
				}
			}
			if slices.Contains(o.calls, "MoveAccount") || slices.Contains(o.calls, "ListOrganizationalUnitsForParent") {
				t.Fatalf("calls = %v", o.calls)
			}
			noCreations(t, o)
		})
	}
}
