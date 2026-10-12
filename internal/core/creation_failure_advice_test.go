package core

import (
	"strings"
	"testing"
)

// The reasons are the CreateAccountFailureReason values of the Organizations
// SDK (organizations v1.60.0, types/enums.go). Core does not import the SDK.
func TestFailureAdviceNamesANextStep(t *testing.T) {
	for _, tc := range []struct {
		reason string
		admin  bool // the next step belongs to an admin
	}{
		{"ACCOUNT_LIMIT_EXCEEDED", true},
		{"EMAIL_ALREADY_EXISTS", false},
		{"GOVCLOUD_ACCOUNT_ALREADY_EXISTS", false},
		{"INVALID_ADDRESS", true},
		{"INVALID_EMAIL", true},
		{"CONCURRENT_ACCOUNT_MODIFICATION", false},
		{"INTERNAL_FAILURE", false},
		{"MISSING_BUSINESS_VALIDATION", true},
		{"FAILED_BUSINESS_VALIDATION", true},
		{"PENDING_BUSINESS_VALIDATION", false},
		{"INVALID_IDENTITY_FOR_BUSINESS_VALIDATION", true},
		{"UNKNOWN_BUSINESS_VALIDATION", true},
		{"MISSING_PAYMENT_INSTRUMENT", true},
		{"INVALID_PAYMENT_INSTRUMENT", true},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			got := failureAdvice(tc.reason)
			if got == "" || !strings.HasSuffix(got, ".") {
				t.Fatalf("failureAdvice(%q) = %q, want a sentence", tc.reason, got)
			}
			if strings.Contains(got, "An admin") != tc.admin {
				t.Errorf("failureAdvice(%q) = %q, want admin step %v", tc.reason, got, tc.admin)
			}
		})
	}
	// AWS does not describe the first reason, and the others are unknown, so
	// playplace gives no advice for them.
	for _, reason := range []string{"UPDATE_EXISTING_RESOURCE_POLICY_WITH_TAGS_NOT_SUPPORTED", "A_REASON_FROM_A_LATER_SDK", ""} {
		if got := failureAdvice(reason); got != "" {
			t.Errorf("failureAdvice(%q) = %q, want none", reason, got)
		}
	}
}
