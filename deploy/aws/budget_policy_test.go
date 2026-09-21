package aws_test

import (
	"encoding/json"
	"os"
	"path"
	"testing"
)

func TestBudgetProvisioningPolicyCoverage(t *testing.T) {
	b, err := os.ReadFile("budget-provisioning-deny-scp.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 10240 {
		t.Fatal("SCP exceeds documented size limit")
	}
	var policy struct {
		Statement []struct {
			Effect, Resource string
			Action           []string
			Condition        json.RawMessage
		}
	}
	if err := json.Unmarshal(b, &policy); err != nil {
		t.Fatal(err)
	}
	matches := func(action string) bool {
		for _, s := range policy.Statement {
			if s.Effect != "Deny" || s.Resource != "*" || len(s.Condition) != 0 {
				t.Fatal("expected unconditional denies without member-controlled bypass")
			}
			for _, pattern := range s.Action {
				if ok, _ := path.Match(pattern, action); ok {
					return true
				}
			}
		}
		return false
	}
	for _, action := range []string{"ec2:RunInstances", "ec2:CreateVolume", "ec2:RequestSpotInstances", "rds:RestoreDBInstanceFromDBSnapshot", "s3:CreateBucket", "lambda:CreateFunction", "ecs:RunTask", "eks:CreateNodegroup", "autoscaling:SetDesiredCapacity", "cloudformation:UpdateStack", "cloudformation:ExecuteChangeSet", "dynamodb:RestoreTableToPointInTime", "elasticloadbalancing:CreateLoadBalancer", "sagemaker:CreateTrainingJob"} {
		if !matches(action) {
			t.Errorf("missing provisioning deny: %s", action)
		}
	}
	for _, action := range []string{"ec2:DescribeInstances", "ec2:StopInstances", "ec2:TerminateInstances", "rds:DeleteDBInstance", "s3:PutObject", "s3:DeleteObject", "cloudtrail:PutAuditEvents", "cloudtrail:StartLogging", "cloudtrail:GetTrailStatus", "logs:PutLogEvents", "kms:GenerateDataKey", "organizations:CloseAccount"} {
		if matches(action) {
			t.Errorf("unexpected audit/read/cleanup deny: %s", action)
		}
	}
	// Explicit coverage boundary: this is not an AWS-wide resource-creation cap.
	if matches("es:CreateDomain") {
		t.Fatal("update documented coverage if this service is added")
	}
}

func TestBudgetDeploymentExamplesAreJSON(t *testing.T) {
	for _, name := range []string{"budget-actions-trust.json", "budget-actions-execution-policy.json", "playplace-budget-actions-policy.json"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(b) {
			t.Fatalf("invalid JSON: %s", name)
		}
	}
}
