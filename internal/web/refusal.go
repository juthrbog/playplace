package web

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"playplace/internal/core"
)

// The refusal texts of a failed call to AWS. The raw error holds AWS request
// IDs, so people see these words and the log holds the raw error. A throttle
// or a time-out passes when the person waits, so its text says only that.
const (
	awsFailed = "The call to AWS failed. Try again in a minute. If it fails again, ask an admin to read the playplace log."
	awsSlow   = "AWS did not answer in time. Try again in a minute."
)

// The texts of a failed call to AWS in the failed panel of the account list.
// The panel adds that the page tries again by itself, so these texts do not
// tell the person to try again.
const (
	listFailed = "The call to AWS failed. If it keeps failing, ask an admin to read the playplace log."
	listSlow   = "AWS did not answer in time."
)

// slowCodes are the AWS error codes of a throttle or a time-out. The AWS SDK
// retries them before it gives up: DefaultThrottleErrorCodes and
// DefaultRetryableErrorCodes in aws/retry/standard.go of aws-sdk-go-v2. The
// list keeps the codes of the services that playplace calls. Cost Explorer
// uses LimitExceededException for too many calls.
var slowCodes = map[string]bool{
	"Throttling":                true,
	"ThrottlingException":       true,
	"ThrottledException":        true,
	"RequestThrottledException": true,
	"TooManyRequestsException":  true,
	"LimitExceededException":    true,
	"RequestTimeout":            true,
	"RequestTimeoutException":   true,
}

// awsCoded is an error that carries an AWS error code. smithy.APIError of
// smithy-go has this method, and the SDK finds a code the same way
// (aws/retry/throttle_error.go).
type awsCoded interface{ ErrorCode() string }

// refusal is the text that people see when err refuses or fails an action.
// request names the request that the action was on. It is empty when the
// action was on no request. The checks run in order, so a failed call to
// AWS that wraps a sentinel of core reads as the sentinel. A failed call to
// AWS never shows its raw text. The caller logs the raw error.
func refusal(err error, request string) string {
	var self core.SelfApprovalError
	var owner core.OwnerLimitError
	var lifetime core.LifetimeLimitError
	var failed *core.ProviderError
	var coded awsCoded
	switch {
	case request != "" && errors.Is(err, core.ErrRequestChanged):
		return request + " changed after you loaded the page. Read the new values, then try again."
	case request != "" && errors.As(err, &self):
		if self.WouldOwn {
			return request + " names you as the owner, so you cannot approve it. Another approver must approve it."
		}
		return "You requested " + request + ", so you cannot approve it. Another approver must approve it."
	case request != "" && errors.Is(err, core.ErrNotFound):
		return request + " is no longer waiting. Someone else decided it, the owner withdrew it, or it was dropped."
	// The limits start with an email or a name, which keep their letters.
	case errors.As(err, &owner):
		held := fmt.Sprintf("%d open accounts or requests", owner.Held)
		if owner.Held == 1 {
			held = "1 open account or request"
		}
		return fmt.Sprintf("%s already holds %s. The limit is %d.", owner.Owner, held, owner.Limit)
	case errors.As(err, &lifetime):
		return fmt.Sprintf("%s can live at most %s. The latest expiry is %s. An admin can override the limit.",
			lifetime.Name, core.Lifetime(lifetime.Max), lifetime.Latest.UTC().Format("2006-01-02 15:04 UTC"))
	// An error with an AWS code comes from AWS also when core did not mark
	// it. A time limit of playplace that ends a call reads as a slow answer.
	case errors.As(err, &failed), errors.As(err, &coded), errors.Is(err, context.DeadlineExceeded):
		if slow(err) {
			return awsSlow
		}
		return awsFailed
	}
	// Core writes its refusals for people, for example the checks of a
	// request and the rules of an extension.
	return sentences(err.Error())
}

// listFailure is the text of the failed panel of the account list. It is
// the text of refusal, but a failed call to AWS gets the words of the panel.
func listFailure(err error) string {
	switch text := refusal(err, ""); text {
	case awsFailed:
		return listFailed
	case awsSlow:
		return listSlow
	default:
		return text
	}
}

// storedFailure is the text that a page shows for an error that core stored
// in a tag or in history. A stored failure of an AWS call holds the raw SDK
// text and its request ID, so the page shows awsFailed in its place. The
// SDK writes "operation error" before the name of the call, and the HTTP
// layer writes "RequestID:".
func storedFailure(text string) string {
	if strings.Contains(text, "operation error ") || strings.Contains(text, "RequestID:") {
		return awsFailed
	}
	return text
}

// slow reports whether err is a throttle or a time-out. A time-out reports
// itself with Timeout, as net.Error does. context.DeadlineExceeded has this
// method too.
func slow(err error) bool {
	var coded awsCoded
	if errors.As(err, &coded) && slowCodes[coded.ErrorCode()] {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}
