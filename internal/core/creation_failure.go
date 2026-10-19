package core

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// reportCreationFailure records, once, that AWS could not create the account
// of the approved record r, and sends the notice. cr is the create status of
// r and must be CreateFailed. The caller holds opMu.
func (s *Service) reportCreationFailure(ctx context.Context, r Request, cr CreateRequest) error {
	// r can be a whole pass old, and opMu does not cover other processes.
	// Write the live record, and only while it is still this unreported
	// failure, so that a request queued under the name since then is not
	// overwritten. Tags have no compare-and-swap, so a small gap remains.
	live, err := s.allRequests(ctx)
	if err != nil {
		return err
	}
	var cur Request
	ok := false
	for _, l := range live {
		if l.Name == r.Name {
			cur, ok = l, true
			break
		}
	}
	if !ok || !cur.Approved || cur.JourneyID != r.JourneyID || cur.CreateRequestID != r.CreateRequestID || cur.FailureReported {
		return nil // another process reported it or replaced the record
	}
	flagged := cur
	flagged.FailureReported = true
	if utf8.RuneCountInString(flagged.Encode()) > MaxTagValue {
		// The flag must fit, or the failure is never reported. The
		// requested event in history keeps the purpose.
		flagged.Purpose = ""
	}
	// A failed durable write must not notify. As with the ready notice, a
	// crash or a lost history write after this write can lose, not repeat,
	// the event and the notice.
	if err := s.provider.SetOUTags(ctx, map[string]string{flagged.Key(): flagged.Encode()}); err != nil {
		return providerError("record failed creation of "+r.Name, err)
	}
	s.Invalidate()
	a := FromCreateRequest(cr)
	cur.fill(a)
	s.announceCreationFailure(ctx, a, cr)
	return nil
}

// announceCreationFailure writes the failed event of a creation and sends
// its notice. a is the creation as FromCreateRequest shows it, with the
// journey and owner that the caller knows.
func (s *Service) announceCreationFailure(ctx context.Context, a *Account, cr CreateRequest) {
	s.audit(ctx, "failed", a, ActorSystem, "", fmt.Sprintf("AWS could not create %s: %s", a.Name, cr.FailureReason),
		map[string]string{"reason": cr.FailureReason, "request_id": cr.RequestID})
	body := fmt.Sprintf("AWS could not create %s for %s: %s.", a.Name, a.Owner, cr.FailureReason)
	if advice := failureAdvice(cr.FailureReason); advice != "" {
		body += " " + advice
	}
	s.notifyAll(ctx, "Playground account creation failed", body, a.Owner, a.ID)
}

// reportReplacedFailure reports the failed creation of the approved record
// under name before a new request overwrites it. checkNameFree lets a new
// request take the name of a failed creation, and both records share one
// tag key. The failed row then shows no owner for the rest of
// FailedRetention; history and the notice already hold the facts. The
// caller holds opMu.
func (s *Service) reportReplacedFailure(ctx context.Context, name string) error {
	records, err := s.allRequests(ctx)
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Name != name || !r.Approved || r.FailureReported || r.CreateRequestID == "" {
			continue
		}
		creates, err := s.provider.ListCreateRequests(ctx)
		if err != nil {
			return providerError("list create requests", err)
		}
		for _, c := range creates {
			if c.RequestID == r.CreateRequestID && c.State == CreateFailed {
				return s.reportCreationFailure(ctx, r, c)
			}
		}
		return nil
	}
	return nil
}

// failureAdvice names the next step after AWS refused a creation with the
// given FailureReason, and who takes it, or "" when AWS documents no meaning
// for the reason. The reasons are the CreateAccountFailureReason values of
// the Organizations API; core keeps them as strings.
func failureAdvice(reason string) string {
	switch reason {
	case "EMAIL_ALREADY_EXISTS", "GOVCLOUD_ACCOUNT_ALREADY_EXISTS":
		// AWS never frees a root email, and --email-pattern derives the
		// email from the name.
		return "Another AWS account already uses the root email of this name. Request the account again with another name."
	case "INVALID_EMAIL":
		return "AWS rejected the root email of this name. An admin must correct --email-pattern, or the account can be requested again with another name."
	case "INVALID_ADDRESS":
		return "AWS rejected the contact address of the organization. An admin must correct the address of the management account before the account is requested again."
	case "ACCOUNT_LIMIT_EXCEEDED":
		return "The organization reached its account limit, and a closed account counts toward it for 90 days. An admin can ask for a higher limit in the Service Quotas console."
	case "INTERNAL_FAILURE":
		return "This is a temporary AWS failure. Request the account again."
	case "CONCURRENT_ACCOUNT_MODIFICATION":
		// Another request with the same details can still create the account.
		return "AWS was already handling a request with the same details. Request the account again only if no account with this name appears in a few minutes."
	case "PENDING_BUSINESS_VALIDATION":
		return "AWS is still validating the business details of the management account. Request the account again when the validation is complete."
	case "MISSING_BUSINESS_VALIDATION", "FAILED_BUSINESS_VALIDATION", "INVALID_IDENTITY_FOR_BUSINESS_VALIDATION", "UNKNOWN_BUSINESS_VALIDATION":
		return "AWS needs valid business details for the management account. An admin must complete them in the AWS console before the account is requested again."
	case "MISSING_PAYMENT_INSTRUMENT", "INVALID_PAYMENT_INSTRUMENT":
		return "The management account needs a valid payment method. An admin must add one in the AWS Billing console before the account is requested again."
	default:
		// For example UPDATE_EXISTING_RESOURCE_POLICY_WITH_TAGS_NOT_SUPPORTED,
		// which AWS does not describe, or a reason added after this code.
		return ""
	}
}
