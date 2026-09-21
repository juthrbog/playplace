package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	ot "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"playplace/internal/core"
)

// RetireBudgetAction intentionally does not validate limit, email, recovery,
// budget period, or SCP attachment capacity: none should prevent retirement.
// The action must still match our ownership tags, single target, policy and role.
func (p *Provider) RetireBudgetAction(ctx context.Context, id string, spec core.BudgetSpec) (bool, error) {
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return false, err
	}
	role, err := arn.Parse(spec.RoleARN)
	if err != nil || role.Service != "iam" || role.AccountID != info.ManagementAccountID || !strings.HasPrefix(role.Resource, "role/") || spec.PolicyID == "" {
		return false, errors.New("restore the budget policy and management-account execution role before retirement")
	}
	if id == info.ManagementAccountID {
		return false, errors.New("refusing to retire a management-account budget action")
	}
	closed := func() error {
		out, err := p.orgs.DescribeAccount(ctx, &organizations.DescribeAccountInput{AccountId: aws.String(id)})
		if err != nil {
			return err
		}
		if out.Account == nil || aws.ToString(out.Account.Id) != id || out.Account.State != ot.AccountStateClosed {
			return errors.New("budget action retirement requires a fresh AWS account State of CLOSED")
		}
		return nil
	}
	if err := closed(); err != nil {
		return false, err
	}
	tags, err := p.tagsFor(ctx, id)
	if err != nil {
		return false, err
	}
	if tags[core.TagManaged] != "true" || (tags[core.TagBudgetPolicy] != "" && tags[core.TagBudgetPolicy] != spec.PolicyID) {
		return false, errors.New("account ownership or budget policy has drifted; refusing retirement")
	}
	parents, err := p.orgs.ListParents(ctx, &organizations.ListParentsInput{ChildId: aws.String(id)})
	if err != nil {
		return false, err
	}
	if len(parents.Parents) != 1 || aws.ToString(parents.Parents[0].Id) != info.PlaygroundOUID {
		return false, errors.New("budget retirement target is not directly in the playground OU")
	}
	a, err := p.findBudgetActionAllowMissing(ctx, info, id, spec, true)
	if err != nil {
		return false, err
	}
	if a == nil {
		return true, nil
	}
	if aws.ToString(a.ActionId) == "" {
		return false, errors.New("owned budget action has no ID; refusing retirement")
	}
	// Discovery may involve several pages. Recheck closure immediately before
	// deletion, not just against the worker's earlier inventory snapshot.
	if err := closed(); err != nil {
		return false, err
	}
	_, err = p.budgets.DeleteBudgetAction(ctx, &budgets.DeleteBudgetActionInput{
		AccountId: aws.String(info.ManagementAccountID), BudgetName: budgetName(id), ActionId: a.ActionId,
	})
	var missing *bt.NotFoundException
	if err != nil && !errors.As(err, &missing) {
		return false, fmt.Errorf("delete owned budget action: %w", err)
	}
	// An accepted delete (or a concurrent NotFound) is not observed absence.
	// ResourceLocked/permission errors are surfaced and retried; never reverse,
	// reset, delete the whole budget, or directly detach any policy to unblock it.
	return false, nil
}
