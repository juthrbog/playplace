package audit

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"playplace/internal/core"
)

// No AWS configuration, environment credentials, instance metadata or default
// credential chain loads. The opt-in endpoint must be explicitly loopback.
func localDynamoClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	endpoint := os.Getenv("PLAYPLACE_TEST_DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PLAYPLACE_TEST_DYNAMODB_ENDPOINT to an isolated loopback DynamoDB Local endpoint")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		t.Fatal("test DynamoDB endpoint must be plain HTTP on loopback")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	return dynamodb.NewFromConfig(aws.Config{
		Region: "us-east-1", RetryMaxAttempts: 1,
		HTTPClient: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
		}),
	}, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// TestDynamoDBLocalContract creates/deletes only its randomly named table,
// never an existing deployment table.
func TestDynamoDBLocalContract(t *testing.T) {
	client := localDynamoClient(t)
	table := "playplace-history-test-" + core.NewHistoryID()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(table), BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}); err != nil {
			t.Errorf("delete test table %s: %v", table, err)
		}
	})
	if err := dynamodb.NewTableExistsWaiter(client).Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	dynamoContract(t, client, table)
	t.Run("producer process and disk discarded", func(t *testing.T) {
		ns := Namespace{Organization: core.NewHistoryID(), Environment: "subprocess"}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestDynamoDBLocalProducer$", "-test.v")
		cmd.Env = append(os.Environ(), "PLAYPLACE_TEST_HISTORY_TABLE="+table, "PLAYPLACE_TEST_HISTORY_ORG="+ns.Organization)
		cmd.Dir = t.TempDir()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("producer: %v\n%s", err, out)
		}
		if err := os.RemoveAll(cmd.Dir); err != nil {
			t.Fatal(err)
		}
		// A new SDK client and adapter read only the shared store. Neither the
		// producer's memory/disk nor a queue/account inventory is available.
		d := testStore(t, localDynamoClient(t), table, ns)
		for _, c := range []Correlation{{CorrelationOperation, "operation"}, {CorrelationProviderRequest, "car-direct"}} {
			j, err := d.ResolveJourney(ctx, c)
			if err != nil || j.ID != "direct" || j.Origin != OriginDirect || j.Terminal != nil {
				t.Fatalf("restart: %+v %v", j, err)
			}
			p, err := d.Search(ctx, Filter{JourneyID: j.ID})
			if err != nil || len(p.Events) != 1 || p.Events[0].Event != "failed" {
				t.Fatalf("restart history: %+v %v", p, err)
			}
		}
	})
}

// The parent contract invokes this test in a disposable producer process.
func TestDynamoDBLocalProducer(t *testing.T) {
	table, org := os.Getenv("PLAYPLACE_TEST_HISTORY_TABLE"), os.Getenv("PLAYPLACE_TEST_HISTORY_ORG")
	if table == "" || org == "" {
		t.Skip("subprocess helper")
	}
	if !strings.HasPrefix(table, "playplace-history-test-") {
		t.Fatal("producer requires a test table")
	}
	d := testStore(t, localDynamoClient(t), table, Namespace{org, "subprocess"})
	j := journey("direct", OriginDirect)
	j.Correlations = []Correlation{{CorrelationOperation, "operation"}}
	j = saveJourney(t, d, j)
	j.Correlations = append(j.Correlations, Correlation{CorrelationProviderRequest, "car-direct"})
	saveJourney(t, d, j)
	if err := d.Record(context.Background(), core.AuditEvent{ID: "failed", JourneyID: j.ID, At: historyStart, Event: "failed", Account: j.Name}); err != nil {
		t.Fatal(err)
	}
}
