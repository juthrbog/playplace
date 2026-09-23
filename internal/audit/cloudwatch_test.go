package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"playplace/internal/core"
)

func TestCloudWatchSearchReadsBeyondOldCapAndMatchesCaseLocally(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			NextToken     string `json:"nextToken"`
			FilterPattern string `json:"filterPattern"`
			StartTime     *int64 `json:"startTime"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if in.FilterPattern != "" {
			t.Errorf("case-insensitive owner must not be narrowed by server equality: %s", in.FilterPattern)
		}
		if in.StartTime != nil {
			t.Errorf("unexpected implicit history window: %v", *in.StartTime)
		}
		calls++
		events := []map[string]string{}
		response := map[string]any{}
		if in.NextToken == "" {
			for i := 0; i < 5001; i++ {
				b, _ := json.Marshal(core.AuditEvent{ID: fmt.Sprintf("%05d", i), JourneyID: "journey", At: at, Event: "requested", Owner: "bob"})
				events = append(events, map[string]string{"message": string(b)})
			}
			response["nextToken"] = "later"
		} else if in.NextToken == "later" {
			b, _ := json.Marshal(core.AuditEvent{ID: "latest", JourneyID: "journey", At: at.Add(time.Hour), Event: "approved", Owner: "bob"})
			events = append(events, map[string]string{"message": string(b)}, map[string]string{"message": "{}"})
		} else {
			t.Errorf("unexpected token %q", in.NextToken)
		}
		response["events"] = events
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer ts.Close()
	client := cloudwatchlogs.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
	}), RetryMaxAttempts: 1}, func(o *cloudwatchlogs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	store := &CloudWatch{Client: client, Group: "history"}
	p, err := store.Search(context.Background(), Filter{Owner: "BOB", Limit: 1})
	if err != nil || len(p.Events) != 1 || p.Events[0].ID != "latest" || p.Next == "" || !p.Incomplete || calls != 2 {
		t.Fatalf("search=%+v err=%v calls=%d", p, err, calls)
	}
}
