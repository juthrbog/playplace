package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	idstypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"

	"playplace/internal/core"
)

// idc holds the resolved IAM Identity Center facts, looked up once.
type idc struct {
	instanceArn      string
	identityStoreID  string
	permissionSetArn string
}

func (p *Provider) AccessEnabled() bool { return p.cfg.PermissionSet != "" }

// identityCenter resolves the instance and the configured permission set.
func (p *Provider) identityCenter(ctx context.Context) (idc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idc != nil {
		return *p.idc, nil
	}
	inst, err := p.sso.ListInstances(ctx, &ssoadmin.ListInstancesInput{})
	if err != nil {
		return idc{}, fmt.Errorf("list identity center instances: %w", err)
	}
	if len(inst.Instances) == 0 {
		return idc{}, errors.New("IAM Identity Center is not enabled in this organization")
	}
	c := idc{
		instanceArn:     aws.ToString(inst.Instances[0].InstanceArn),
		identityStoreID: aws.ToString(inst.Instances[0].IdentityStoreId),
	}
	// An ARN needs no lookup; a name is resolved by listing and describing.
	if strings.HasPrefix(p.cfg.PermissionSet, "arn:") {
		c.permissionSetArn = p.cfg.PermissionSet
		p.idc = &c
		return c, nil
	}
	pag := ssoadmin.NewListPermissionSetsPaginator(p.sso, &ssoadmin.ListPermissionSetsInput{InstanceArn: aws.String(c.instanceArn)})
	for pag.HasMorePages() && c.permissionSetArn == "" {
		page, err := pag.NextPage(ctx)
		if err != nil {
			return idc{}, fmt.Errorf("list permission sets: %w", err)
		}
		for _, arn := range page.PermissionSets {
			d, err := p.sso.DescribePermissionSet(ctx, &ssoadmin.DescribePermissionSetInput{InstanceArn: aws.String(c.instanceArn), PermissionSetArn: aws.String(arn)})
			if err != nil {
				return idc{}, fmt.Errorf("describe permission set: %w", err)
			}
			if aws.ToString(d.PermissionSet.Name) == p.cfg.PermissionSet {
				c.permissionSetArn = arn
				break
			}
		}
	}
	if c.permissionSetArn == "" {
		return idc{}, fmt.Errorf("permission set %q not found in Identity Center; create it first", p.cfg.PermissionSet)
	}
	p.idc = &c
	return c, nil
}

// LookupUser tries the identity as an email, then as a user name.
func (p *Provider) LookupUser(ctx context.Context, identity string) (core.User, error) {
	c, err := p.identityCenter(ctx)
	if err != nil {
		return core.User{}, err
	}
	identity = strings.TrimSpace(identity)
	paths := []string{"userName"}
	if strings.Contains(identity, "@") {
		paths = []string{"emails.value", "userName"}
	}
	var userID string
	for _, path := range paths {
		out, err := p.ids.GetUserId(ctx, &identitystore.GetUserIdInput{
			IdentityStoreId: aws.String(c.identityStoreID),
			AlternateIdentifier: &idstypes.AlternateIdentifierMemberUniqueAttribute{
				Value: idstypes.UniqueAttribute{AttributePath: aws.String(path), AttributeValue: docString(identity)},
			},
		})
		var nf *idstypes.ResourceNotFoundException
		if errors.As(err, &nf) {
			continue
		}
		if err != nil {
			return core.User{}, fmt.Errorf("look up user %q: %w", identity, err)
		}
		userID = aws.ToString(out.UserId)
		break
	}
	if userID == "" {
		return core.User{}, core.ErrUserNotFound
	}
	u, err := p.ids.DescribeUser(ctx, &identitystore.DescribeUserInput{IdentityStoreId: aws.String(c.identityStoreID), UserId: aws.String(userID)})
	if err != nil {
		return core.User{}, fmt.Errorf("describe user %s: %w", userID, err)
	}
	user := core.User{ID: userID, UserName: aws.ToString(u.UserName)}
	for _, e := range u.Emails {
		if user.Email == "" || e.Primary {
			user.Email = aws.ToString(e.Value)
		}
	}
	return user, nil
}

// GrantAccess assigns the permission set to the user on the account and
// waits for Identity Center to provision it.
func (p *Provider) GrantAccess(ctx context.Context, providerID string, user core.User) error {
	ctx, cancel := context.WithTimeout(ctx, assignmentTimeout)
	defer cancel()
	c, err := p.identityCenter(ctx)
	if err != nil {
		return err
	}
	var out *ssoadmin.CreateAccountAssignmentOutput
	for attempt := 0; ; attempt++ {
		out, err = p.sso.CreateAccountAssignment(ctx, &ssoadmin.CreateAccountAssignmentInput{
			InstanceArn:      aws.String(c.instanceArn),
			PermissionSetArn: aws.String(c.permissionSetArn),
			PrincipalId:      aws.String(user.ID),
			PrincipalType:    ssotypes.PrincipalTypeUser,
			TargetId:         aws.String(providerID),
			TargetType:       ssotypes.TargetTypeAwsAccount,
		})
		var conflict *ssotypes.ConflictException
		if errors.As(err, &conflict) && attempt < 5 {
			// Another assignment is in flight for this account; back off.
			if err := sleep(ctx, time.Duration(attempt+1)*2*time.Second); err != nil {
				return err
			}
			continue
		}
		break
	}
	if err != nil {
		return fmt.Errorf("create account assignment: %w", err)
	}
	st := out.AccountAssignmentCreationStatus
	for st.Status == ssotypes.StatusValuesInProgress {
		if err := sleep(ctx, 3*time.Second); err != nil {
			return err
		}
		d, err := p.sso.DescribeAccountAssignmentCreationStatus(ctx, &ssoadmin.DescribeAccountAssignmentCreationStatusInput{
			InstanceArn:                        aws.String(c.instanceArn),
			AccountAssignmentCreationRequestId: st.RequestId,
		})
		if err != nil {
			return fmt.Errorf("describe assignment status: %w", err)
		}
		st = d.AccountAssignmentCreationStatus
	}
	if st.Status == ssotypes.StatusValuesFailed {
		return fmt.Errorf("account assignment failed: %s", aws.ToString(st.FailureReason))
	}
	return nil
}

// RevokeAccess deletes the assignment. A missing assignment is not an error.
func (p *Provider) RevokeAccess(ctx context.Context, providerID string, user core.User) error {
	ctx, cancel := context.WithTimeout(ctx, assignmentTimeout)
	defer cancel()
	c, err := p.identityCenter(ctx)
	if err != nil {
		return err
	}
	out, err := p.sso.DeleteAccountAssignment(ctx, &ssoadmin.DeleteAccountAssignmentInput{
		InstanceArn:      aws.String(c.instanceArn),
		PermissionSetArn: aws.String(c.permissionSetArn),
		PrincipalId:      aws.String(user.ID),
		PrincipalType:    ssotypes.PrincipalTypeUser,
		TargetId:         aws.String(providerID),
		TargetType:       ssotypes.TargetTypeAwsAccount,
	})
	var nf *ssotypes.ResourceNotFoundException
	if errors.As(err, &nf) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete account assignment: %w", err)
	}
	st := out.AccountAssignmentDeletionStatus
	for st.Status == ssotypes.StatusValuesInProgress {
		if err := sleep(ctx, 3*time.Second); err != nil {
			return err
		}
		d, err := p.sso.DescribeAccountAssignmentDeletionStatus(ctx, &ssoadmin.DescribeAccountAssignmentDeletionStatusInput{
			InstanceArn:                        aws.String(c.instanceArn),
			AccountAssignmentDeletionRequestId: st.RequestId,
		})
		if err != nil {
			return fmt.Errorf("describe deletion status: %w", err)
		}
		st = d.AccountAssignmentDeletionStatus
	}
	if st.Status == ssotypes.StatusValuesFailed {
		return fmt.Errorf("account assignment deletion failed: %s", aws.ToString(st.FailureReason))
	}
	return nil
}

// assignmentTimeout bounds the wait for Identity Center to finish an
// assignment. The worker has its own pass timeout; the CLI and TUI do not,
// and an assignment stuck IN_PROGRESS must not hold them forever.
const assignmentTimeout = 5 * time.Minute

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
