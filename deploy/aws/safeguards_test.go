package aws_test

import (
	"encoding/json"
	"os"
	"path"
	"testing"
)

// Offline regression checks for the shipped deny-list, not an IAM simulator.
// Validate the policy with AWS Access Analyzer before attaching it.
func TestPlaygroundSafeguards(t *testing.T) {
	b, err := os.ReadFile("playground-safeguards-scp.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 5120 {
		t.Fatal("SCP exceeds the 5120-character limit")
	}
	var policy struct {
		Version   string
		Statement []struct {
			Sid, Effect, Resource string
			Action                json.RawMessage
			Condition             json.RawMessage
		}
	}
	if err := json.Unmarshal(b, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Version != "2012-10-17" {
		t.Fatalf("version %q", policy.Version)
	}
	var denied []string
	seen := map[string]bool{}
	for _, s := range policy.Statement {
		if s.Sid == "" || seen[s.Sid] || s.Effect != "Deny" || s.Resource != "*" || len(s.Condition) != 0 {
			t.Fatalf("expected unique, unconditional, all-resource deny: %+v", s)
		}
		seen[s.Sid] = true
		var actions []string
		if err := json.Unmarshal(s.Action, &actions); err != nil {
			var action string
			if err := json.Unmarshal(s.Action, &action); err != nil {
				t.Fatal(err)
			}
			actions = []string{action}
		}
		denied = append(denied, actions...)
	}
	matches := func(action string) bool {
		for _, pattern := range denied {
			ok, err := path.Match(pattern, action)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				return true
			}
		}
		return false
	}
	for _, action := range []string{
		"savingsplans:CreateSavingsPlan", "ec2:PurchaseReservedInstancesOffering",
		"ec2:AcceptReservedInstancesExchangeQuote", "ec2:PurchaseHostReservation",
		"ec2:PurchaseScheduledInstances", "ec2:PurchaseCapacityBlock", "ec2:PurchaseCapacityBlockExtension",
		"rds:PurchaseReservedDBInstancesOffering", "redshift:PurchaseReservedNodeOffering",
		"redshift:AcceptReservedNodeExchange", "elasticache:PurchaseReservedCacheNodesOffering",
		"memorydb:PurchaseReservedNodesOffering", "es:PurchaseReservedInstanceOffering",
		"es:PurchaseReservedElasticsearchInstanceOffering", "dynamodb:PurchaseReservedCapacityOfferings",
		"mediaconnect:PurchaseOffering", "medialive:PurchaseOffering", "sagemaker:CreateTrainingPlan",
		"aws-marketplace:Subscribe", "aws-marketplace:CreateAgreementRequest",
		"aws-marketplace:AcceptAgreementRequest", "aws-marketplace:AcceptAgreementApprovalRequest",
		"aws-marketplace:AcceptAgreementPaymentRequest", "cloudtrail:CreateTrail", "organizations:LeaveOrganization",
	} {
		if !matches(action) {
			t.Errorf("missing deny for %s", action)
		}
	}
	// Do not prevent inspection, cancellation, authorized cleanup of existing
	// account trails, or ordinary on-demand playground usage.
	for _, action := range []string{
		"ec2:RunInstances", "ec2:TerminateInstances", "s3:PutObject",
		"aws-marketplace:ViewSubscriptions", "aws-marketplace:Unsubscribe",
		"aws-marketplace:CancelAgreement", "aws-marketplace:AcceptAgreementCancellationRequest",
		"savingsplans:DescribeSavingsPlans", "savingsplans:DeleteQueuedSavingsPlan",
		"cloudtrail:DescribeTrails", "cloudtrail:GetTrailStatus", "cloudtrail:DeleteTrail",
		"cloudtrail:StopLogging", "cloudtrail:LookupEvents", "organizations:DescribeAccount",
	} {
		if matches(action) {
			t.Errorf("unexpected deny for %s", action)
		}
	}
}
