package aws

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	bt "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	ot "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"playplace/internal/core"
)

func (p *Provider) validateBudgetTarget(ctx context.Context, info core.OrgInfo, id string, spec core.BudgetSpec) error {
	role, err := arn.Parse(spec.RoleARN)
	if err != nil || role.Service != "iam" || role.AccountID != info.ManagementAccountID || !strings.HasPrefix(role.Resource, "role/") {
		return errors.New("budget-action-role must be an IAM role in the management account")
	}
	if id == info.ManagementAccountID {
		return errors.New("refusing to restrict the management account")
	}
	parents, err := p.orgs.ListParents(ctx, &organizations.ListParentsInput{ChildId: aws.String(id)})
	if err != nil {
		return err
	}
	if len(parents.Parents) != 1 || aws.ToString(parents.Parents[0].Id) != info.PlaygroundOUID {
		return errors.New("budget target is not directly in the playground OU")
	}
	roots, err := p.orgs.ListRoots(ctx, &organizations.ListRootsInput{})
	if err != nil {
		return err
	}
	enabled := false
	for _, root := range roots.Roots {
		if aws.ToString(root.Id) == info.RootID {
			for _, t := range root.PolicyTypes {
				if t.Type == ot.PolicyTypeServiceControlPolicy && t.Status == ot.PolicyTypeStatusEnabled {
					enabled = true
				}
			}
		}
	}
	if !enabled {
		return errors.New("service control policies must be enabled separately on the organization root")
	}
	policy, err := p.orgs.DescribePolicy(ctx, &organizations.DescribePolicyInput{PolicyId: aws.String(spec.PolicyID)})
	if err != nil {
		return err
	}
	if policy.Policy == nil || policy.Policy.PolicySummary == nil || policy.Policy.PolicySummary.Type != ot.PolicyTypeServiceControlPolicy || policy.Policy.PolicySummary.AwsManaged {
		return errors.New("budget-scp-id must identify a customer-managed SCP")
	}
	// An OU/root attachment cannot be reversed by a per-account budget action.
	tp := organizations.NewListTargetsForPolicyPaginator(p.orgs, &organizations.ListTargetsForPolicyInput{PolicyId: aws.String(spec.PolicyID)})
	for tp.HasMorePages() {
		page, err := tp.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, target := range page.Targets {
			if target.Type != ot.TargetTypeAccount {
				return errors.New("budget restriction SCP must not be attached to an OU or root")
			}
		}
	}
	return nil
}

func (p *Provider) budgetAttachment(ctx context.Context, id, policy string) (bool, error) {
	attached, count := false, 0
	pag := organizations.NewListPoliciesForTargetPaginator(p.orgs, &organizations.ListPoliciesForTargetInput{TargetId: aws.String(id), Filter: ot.PolicyTypeServiceControlPolicy})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, x := range page.Policies {
			count++
			if aws.ToString(x.Id) == policy {
				attached = true
			}
		}
	}
	if !attached && count >= 10 {
		return false, errors.New("no free direct SCP attachment slot for the budget action (10-policy quota)")
	}
	return attached, nil
}

func (p *Provider) findBudgetAction(ctx context.Context, info core.OrgInfo, id string, spec core.BudgetSpec) (*bt.Action, error) {
	return p.findBudgetActionAllowMissing(ctx, info, id, spec, false)
}

func (p *Provider) findBudgetActionAllowMissing(ctx context.Context, info core.OrgInfo, id string, spec core.BudgetSpec, allowMissingBudget bool) (*bt.Action, error) {
	role, err := arn.Parse(spec.RoleARN)
	if err != nil {
		return nil, err
	}
	var found *bt.Action
	firstPage := true
	pag := budgets.NewDescribeBudgetActionsForBudgetPaginator(p.budgets, &budgets.DescribeBudgetActionsForBudgetInput{AccountId: aws.String(info.ManagementAccountID), BudgetName: budgetName(id)})
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			// Only a missing budget from this list operation proves absence.
			// A NotFound from an action's tag read below must be retried.
			var missing *bt.NotFoundException
			if allowMissingBudget && firstPage && errors.As(err, &missing) {
				return nil, nil
			}
			return nil, err
		}
		firstPage = false
		for _, action := range page.Actions {
			resource := fmt.Sprintf("arn:%s:budgets::%s:budget/%s/action/%s", role.Partition, info.ManagementAccountID, aws.ToString(budgetName(id)), aws.ToString(action.ActionId))
			tags, err := p.budgets.ListTagsForResource(ctx, &budgets.ListTagsForResourceInput{ResourceARN: aws.String(resource)})
			if err != nil {
				return nil, err
			}
			managed, account := false, ""
			for _, tag := range tags.ResourceTags {
				if aws.ToString(tag.Key) == core.TagManaged && aws.ToString(tag.Value) == "true" {
					managed = true
				}
				if aws.ToString(tag.Key) == "playplace:account" {
					account = aws.ToString(tag.Value)
				}
			}
			if !managed {
				if action.Definition != nil && action.Definition.ScpActionDefinition != nil && aws.ToString(action.Definition.ScpActionDefinition.PolicyId) == spec.PolicyID {
					return nil, errors.New("an unowned action uses the restriction policy; review before proceeding")
				}
				continue
			}
			if account != id || action.ActionType != bt.ActionTypeScp || action.Definition == nil || action.Definition.ScpActionDefinition == nil || aws.ToString(action.Definition.ScpActionDefinition.PolicyId) != spec.PolicyID || !reflect.DeepEqual(action.Definition.ScpActionDefinition.TargetIds, []string{id}) || aws.ToString(action.ExecutionRoleArn) != spec.RoleARN {
				return nil, errors.New("owned budget action target, policy or role has drifted; manual review required")
			}
			if found != nil {
				return nil, errors.New("multiple owned budget actions; manual review required")
			}
			x := action
			found = &x
		}
	}
	return found, nil
}

func (p *Provider) reconcileBudgetAction(ctx context.Context, info core.OrgInfo, id string, spec core.BudgetSpec) (core.BudgetProtection, error) {
	r := core.BudgetProtection{State: "setup-pending"}
	a, err := p.findBudgetAction(ctx, info, id, spec)
	if err != nil {
		return r, err
	}
	attached, err := p.budgetAttachment(ctx, id, spec.PolicyID)
	if err != nil {
		return r, err
	}
	account, name := aws.String(info.ManagementAccountID), budgetName(id)
	threshold := &bt.ActionThreshold{ActionThresholdType: bt.ThresholdTypePercentage, ActionThresholdValue: 100}
	subs := []bt.Subscriber{{SubscriptionType: bt.SubscriptionTypeEmail, Address: aws.String(spec.Email)}}
	if a == nil {
		if attached || spec.Recovery != nil {
			return r, errors.New("action missing with restriction or recovery present; refusing to replace its ownership")
		}
		out, err := p.budgets.CreateBudgetAction(ctx, &budgets.CreateBudgetActionInput{
			AccountId: account, BudgetName: name, ActionType: bt.ActionTypeScp, ApprovalModel: bt.ApprovalModelAuto,
			ActionThreshold: threshold, NotificationType: bt.NotificationTypeActual, ExecutionRoleArn: aws.String(spec.RoleARN), Subscribers: subs,
			Definition:   &bt.Definition{ScpActionDefinition: &bt.ScpActionDefinition{PolicyId: aws.String(spec.PolicyID), TargetIds: []string{id}}},
			ResourceTags: []bt.ResourceTag{{Key: aws.String(core.TagManaged), Value: aws.String("true")}, {Key: aws.String("playplace:account"), Value: aws.String(id)}},
		})
		if err != nil {
			return r, err
		}
		r.ActionID = aws.ToString(out.ActionId)
		return r, nil // Observe STANDBY on the next pass before granting access.
	}
	r.ActionID = aws.ToString(a.ActionId)
	if !reflect.DeepEqual(a.ActionThreshold, threshold) || a.NotificationType != bt.NotificationTypeActual || a.ApprovalModel != bt.ApprovalModelAuto {
		if a.Status != bt.ActionStatusStandby {
			return r, errors.New("budget action configuration drift while not in STANDBY; review before recovery")
		}
		_, err := p.budgets.UpdateBudgetAction(ctx, &budgets.UpdateBudgetActionInput{AccountId: account, BudgetName: name, ActionId: a.ActionId, ActionThreshold: threshold, NotificationType: bt.NotificationTypeActual, ApprovalModel: bt.ApprovalModelAuto, Subscribers: subs})
		return r, err // Confirm on a subsequent pass.
	}
	if !reflect.DeepEqual(a.Subscribers, subs) {
		// Recipient repair must not alter the trigger or approval mode of an
		// executed action. AWS may reject updates while a transition is locked;
		// that failure is visible and retried, never bypassed by detaching a policy.
		_, err := p.budgets.UpdateBudgetAction(ctx, &budgets.UpdateBudgetActionInput{AccountId: account, BudgetName: name, ActionId: a.ActionId, Subscribers: subs})
		return r, err
	}
	// Recovery decisions and phase persistence belong to core. Configuration
	// changes above return without an observation until a subsequent pass.
	r.Action = &core.BudgetActionObservation{Status: string(a.Status), RestrictionAttached: attached}
	return r, nil
}

func (p *Provider) ExecuteBudgetAction(ctx context.Context, id string, spec core.BudgetSpec, actionID string, operation core.BudgetActionOperation) error {
	if operation != core.BudgetActionReverse && operation != core.BudgetActionReset {
		return errors.New("unsupported budget action operation")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	info, err := p.EnsureOrganization(ctx)
	if err != nil {
		return err
	}
	if err := p.validateBudgetTarget(ctx, info, id, spec); err != nil {
		return err
	}
	a, err := p.findBudgetAction(ctx, info, id, spec)
	if err != nil {
		return err
	}
	if a == nil || actionID == "" || aws.ToString(a.ActionId) != actionID {
		return errors.New("budget recovery does not match the owned action")
	}
	_, err = p.budgets.ExecuteBudgetAction(ctx, &budgets.ExecuteBudgetActionInput{
		AccountId: aws.String(info.ManagementAccountID), BudgetName: budgetName(id),
		ActionId: a.ActionId, ExecutionType: bt.ExecutionType(operation),
	})
	return err
}
