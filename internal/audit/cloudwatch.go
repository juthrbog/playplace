package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"playplace/internal/core"
)

// CloudWatch writes events to a log group, one JSON document per event, and
// queries them back with FilterLogEvents and a JSON filter pattern. The
// group is created on first use with the given retention. One stream per
// host and day keeps writers from different machines apart. Safe for
// concurrent use: `serve` records from the worker and from HTTP handlers at
// the same time.
type CloudWatch struct {
	Client        *cloudwatchlogs.Client
	Group         string
	RetentionDays int32
	Window        time.Duration // how far back queries look by default
	Log           *slog.Logger  // optional; warnings that should not fail a write go here

	mu     sync.Mutex
	group  bool   // log group exists
	stream string // stream for the current day, empty until created
	day    string // the day stream belongs to
}

func (c *CloudWatch) Where() string { return "cloudwatch:" + c.Group }
func (c *CloudWatch) Close() error  { return nil }

// ensure makes the group and today's stream exist and returns the stream
// name. Called with c.mu held.
func (c *CloudWatch) ensure(ctx context.Context, now time.Time) (string, error) {
	var exists *cwtypes.ResourceAlreadyExistsException
	if !c.group {
		_, err := c.Client.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{LogGroupName: aws.String(c.Group)})
		if err != nil && !errors.As(err, &exists) {
			return "", fmt.Errorf("create log group %s: %w", c.Group, err)
		}
		if err == nil && c.RetentionDays > 0 {
			if _, rerr := c.Client.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{LogGroupName: aws.String(c.Group), RetentionInDays: aws.Int32(c.RetentionDays)}); rerr != nil && c.Log != nil {
				// History still works without retention; say so once.
				c.Log.Warn("history log group created but retention not set", "group", c.Group, "err", rerr)
			}
		}
		c.group = true
	}
	day := now.UTC().Format("2006-01-02")
	if c.stream != "" && c.day == day {
		return c.stream, nil
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "playplace"
	}
	stream := host + "/" + day
	_, err := c.Client.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{LogGroupName: aws.String(c.Group), LogStreamName: aws.String(stream)})
	if err != nil && !errors.As(err, &exists) {
		return "", fmt.Errorf("create log stream: %w", err)
	}
	c.stream, c.day = stream, day
	return stream, nil
}

func (c *CloudWatch) Record(ctx context.Context, e core.AuditEvent) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// PutLogEvents to one stream must not interleave, and the stream may
	// roll over to a new day, so the whole write runs under the lock.
	c.mu.Lock()
	defer c.mu.Unlock()
	stream, err := c.ensure(ctx, time.Now())
	if err != nil {
		return err
	}
	_, err = c.Client.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName:  aws.String(c.Group),
		LogStreamName: aws.String(stream),
		LogEvents:     []cwtypes.InputLogEvent{{Timestamp: aws.Int64(e.At.UnixMilli()), Message: aws.String(string(line))}},
	})
	if err != nil {
		return fmt.Errorf("put log events: %w", err)
	}
	return nil
}

func (c *CloudWatch) Query(ctx context.Context, f Filter) ([]core.AuditEvent, error) {
	since := f.Since
	if since.IsZero() {
		w := c.Window
		if w <= 0 {
			w = 365 * 24 * time.Hour
		}
		since = time.Now().Add(-w)
	}
	var conds []string
	if f.Account != "" {
		conds = append(conds, fmt.Sprintf(`($.account = %q)`, f.Account))
	}
	if f.Owner != "" {
		conds = append(conds, fmt.Sprintf(`($.owner = %q)`, f.Owner))
	}
	in := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: aws.String(c.Group),
		StartTime:    aws.Int64(since.UnixMilli()),
	}
	if len(conds) > 0 {
		in.FilterPattern = aws.String("{ " + strings.Join(conds, " && ") + " }")
	}
	var out []core.AuditEvent
	pag := cloudwatchlogs.NewFilterLogEventsPaginator(c.Client, in)
	for pag.HasMorePages() {
		page, err := pag.NextPage(ctx)
		if err != nil {
			var nf *cwtypes.ResourceNotFoundException
			if errors.As(err, &nf) {
				return nil, nil // no group yet means no history yet
			}
			return nil, fmt.Errorf("filter log events: %w", err)
		}
		for _, ev := range page.Events {
			var e core.AuditEvent
			if err := json.Unmarshal([]byte(aws.ToString(ev.Message)), &e); err != nil {
				continue
			}
			if f.matches(e) { // LocalStack and older patterns may be lenient
				out = append(out, e)
			}
		}
		if len(out) > 5000 {
			break
		}
	}
	return newestFirst(out, f.limit()), nil
}
