package aws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"playplace/internal/core"
)

func TestFromAccountPrefersState(t *testing.T) {
	for _, tc := range []struct{ state, legacy, want string }{
		{"CLOSED", "SUSPENDED", "CLOSED"},
		{"PENDING_CLOSURE", "ACTIVE", "PENDING_CLOSURE"},
		{"SUSPENDED", "SUSPENDED", "SUSPENDED"},
		{"PENDING_ACTIVATION", "ACTIVE", "PENDING_ACTIVATION"},
		{"ACTIVE", "SUSPENDED", "ACTIVE"},
		{"", "ACTIVE", "ACTIVE"},
		{"", "SUSPENDED", "SUSPENDED"},
		{"", "", ""},
	} {
		t.Run(tc.state+"/"+tc.legacy, func(t *testing.T) {
			r := fromAccount(orgtypes.Account{Id: aws.String("123456789012"), State: orgtypes.AccountState(tc.state), Status: orgtypes.AccountStatus(tc.legacy)}, nil)
			if r.Status != tc.want {
				t.Fatalf("state = %q, want %q", r.Status, tc.want)
			}
		})
	}
}

func TestCloseAccountResponses(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		body           string
		quota, failure bool
	}{
		{"accepted", 200, `{}`, false, false},
		{"already closed", 400, `{"__type":"AccountAlreadyClosedException","Message":"closed"}`, false, false},
		{"quota", 400, `{"__type":"ConstraintViolationException","Reason":"CLOSE_ACCOUNT_QUOTA_EXCEEDED","Message":"quota"}`, true, true},
		{"concurrency", 400, `{"__type":"ConstraintViolationException","Reason":"CLOSE_ACCOUNT_REQUESTS_LIMIT_EXCEEDED","Message":"in flight"}`, true, true},
		{"denied", 400, `{"__type":"AccessDeniedException","Message":"denied"}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Amz-Target") != "AWSOrganizationsV20161128.CloseAccount" {
					t.Errorf("unexpected target %s", r.Header.Get("X-Amz-Target"))
				}
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p := NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
			})}, Config{Endpoint: srv.URL})
			err := p.CloseAccount(context.Background(), "123456789012")
			if (err != nil) != tc.failure || errors.Is(err, core.ErrCloseQuota) != tc.quota {
				t.Fatalf("got %v, failure=%v quota=%v", err, tc.failure, tc.quota)
			}
		})
	}
}
