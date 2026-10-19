package web

import (
	"errors"

	"playplace/internal/core"
)

// The refusal texts of a failed call to AWS. The raw error holds AWS request
// IDs, so people see these words and the log holds the raw error. A throttle
// or a time-out passes when the person waits, so its text says only that.
const (
	awsFailed = "The call to AWS failed. Try again in a minute. If it fails again, ask an admin to read the playplace log."
	awsSlow   = "AWS did not answer in time. Try again in a minute."
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
	var failed *core.ProviderError
	var coded awsCoded
	switch {
	case request != "" && errors.Is(err, core.ErrRequestChanged):
		return request + " changed after you loaded the page. Read the new values, then try again."
	case request != "" && errors.As(err, &self):
		if self.WouldOwn {
			return "You would own " + request + ", so you cannot approve it. Another approver must approve it."
		}
		return "You requested " + request + ", so you cannot approve it. Another approver must approve it."
	case request != "" && errors.Is(err, core.ErrNotFound):
		return request + " is no longer waiting. Someone else decided it, the owner withdrew it, or it was dropped."
	case errors.As(err, &failed), errors.As(err, &coded):
		// An error with an AWS code comes from AWS also when core did not
		// mark it.
		if slow(err) {
			return awsSlow
		}
		return awsFailed
	}
	// Core writes its refusals for people, for example the limits, the
	// checks of a request and the rules of an extension.
	return sentences(err.Error())
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
