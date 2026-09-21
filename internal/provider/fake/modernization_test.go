package fake

import (
	"context"
	"testing"

	"playplace/internal/core"
)

func TestTagCopiesRemainIndependentAndWritable(t *testing.T) {
	ctx := context.Background()
	p := New()
	empty, err := p.OUTags(ctx)
	if err != nil || empty == nil {
		t.Fatalf("empty OU tags must be writable: %v %v", empty, err)
	}
	empty["local"] = "only"
	input := map[string]string{"owner": "original"}
	if err := p.SetOUTags(ctx, input); err != nil {
		t.Fatal(err)
	}
	input["owner"] = "changed"
	first, _ := p.OUTags(ctx)
	first["owner"] = "local edit"
	second, _ := p.OUTags(ctx)
	if second["owner"] != "original" || second["local"] != "" {
		t.Fatalf("OU tags alias a caller's map: %v", second)
	}

	creationTags := map[string]string{"owner": "original"}
	request, err := p.RequestAccount(ctx, core.AccountRequest{Name: "copy-test", Email: "copy@example.com", Tags: creationTags})
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreateStatus(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	creationTags["owner"] = "changed after creation"
	id := created.ProviderID
	if p.Tags(id)["owner"] != "original" {
		t.Fatal("settled account aliases creation tags")
	}
	if err := p.PlaceAccount(ctx, id, map[string]string{"keep": "value"}); err != nil {
		t.Fatal(err)
	}
	updates := map[string]string{"owner": "new owner"}
	if err := p.SetTags(ctx, id, updates); err != nil {
		t.Fatal(err)
	}
	updates["owner"] = "local edit"
	remote, err := p.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	remote.Tags["owner"] = "another local edit"
	if got := p.Tags(id); got["owner"] != "new owner" || got["keep"] != "value" {
		t.Fatalf("merge or copy semantics changed: %v", got)
	}
	nilCopy := cloneRemote(core.RemoteAccount{})
	if nilCopy.Tags == nil {
		t.Fatal("copying nil tags must still produce a writable map")
	}
}

func TestGrantMembershipRemainsIdempotent(t *testing.T) {
	p := New()
	id := "111111111111"
	p.Seed(core.RemoteAccount{ProviderID: id, Status: "ACTIVE"})
	ctx := context.Background()
	for _, user := range []string{"one", "one", "two", "two"} {
		if err := p.GrantAccess(ctx, id, core.User{ID: user}); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.Grants[id]) != 2 || p.Grants[id][0] != "one" || p.Grants[id][1] != "two" {
		t.Fatalf("duplicate grant or changed order: %v", p.Grants[id])
	}
	if err := p.GrantAccess(ctx, "missing", core.User{ID: "one"}); err == nil {
		t.Fatal("granted access to a missing account")
	}
}
