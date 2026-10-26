// Package messaging connects the outbox to the event bus: an SNS publisher
// for the relay and an SQS consumer loop for the worker.
//
// Topology: one SNS topic (duradent-domain-events); each consuming context
// has an SQS queue subscribed with raw delivery and a filter policy on the
// "type" attribute, plus a dead-letter queue after 5 failed receives.
package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/outbox"
)

// --- publisher -----------------------------------------------------------------------

type SNSPublisher struct {
	client   *sns.Client
	topicARN string
}

func NewSNSPublisher(client *sns.Client, topicARN string) *SNSPublisher {
	return &SNSPublisher{client: client, topicARN: topicARN}
}

func (p *SNSPublisher) Publish(ctx context.Context, e outbox.Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = p.client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(p.topicARN),
		Message:  aws.String(string(body)),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"type": {DataType: aws.String("String"), StringValue: aws.String(e.Type)},
		},
	})
	return err
}

// --- consumer ------------------------------------------------------------------------

// Handler processes one event. Returning an error leaves the message in the
// queue: SQS redelivers it after the visibility timeout, and after the
// queue's maxReceiveCount it moves to the dead-letter queue.
type Handler func(ctx context.Context, e outbox.Event) error

var handled = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "events_handled_total", Help: "Events processed by worker consumers, by consumer, type and result.",
}, []string{"consumer", "type", "result"})

func init() { prometheus.MustRegister(handled) }

type Consumer struct {
	name     string
	client   *sqs.Client
	queueURL string
	db       *sql.DB
	handle   Handler
	log      *slog.Logger
}

func NewConsumer(name string, client *sqs.Client, queueURL string, db *sql.DB, h Handler, log *slog.Logger) *Consumer {
	return &Consumer{name: name, client: client, queueURL: queueURL, db: db, handle: h, log: log.With("consumer", name)}
}

// Run long-polls the queue until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(c.queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 20,
		})
		if err != nil {
			if ctx.Err() == nil {
				c.log.ErrorContext(ctx, "receive", "error", err)
				time.Sleep(5 * time.Second)
			}
			continue
		}
		for _, m := range out.Messages {
			if err := c.process(ctx, aws.ToString(m.Body)); err != nil {
				c.log.WarnContext(ctx, "handle message (will retry)", "error", err)
				continue
			}
			if _, err := c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: aws.String(c.queueURL), ReceiptHandle: m.ReceiptHandle}); err != nil {
				c.log.ErrorContext(ctx, "delete message", "error", err)
			}
		}
	}
}

var errUnparseable = errors.New("unparseable message")

// process runs the handler at most once per (consumer, event id). The check
// and the record are not in the same transaction as the handler's external
// side effects (an email, a Nubefact call), so a crash right after the side
// effect may repeat it; handlers are written to tolerate that.
func (c *Consumer) process(ctx context.Context, body string) error {
	var e outbox.Event
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.ID.IsNil() || e.Type == "" {
		// Leave it: after maxReceiveCount it lands in the DLQ for inspection.
		return errUnparseable
	}
	var one int
	err := c.db.QueryRowContext(ctx, "SELECT 1 FROM processed_messages WHERE consumer = ? AND message_id = ?", c.name, e.ID).Scan(&one)
	if err == nil {
		handled.WithLabelValues(c.name, e.Type, "duplicate").Inc()
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := c.handle(ctx, e); err != nil {
		handled.WithLabelValues(c.name, e.Type, "error").Inc()
		return err
	}
	handled.WithLabelValues(c.name, e.Type, "ok").Inc()
	_, err = c.db.ExecContext(ctx, "INSERT IGNORE INTO processed_messages (consumer, message_id, processed_at) VALUES (?, ?, ?)",
		c.name, e.ID, time.Now().UTC())
	return err
}

// Process exposes one-message handling to tests.
func (c *Consumer) Process(ctx context.Context, body string) error { return c.process(ctx, body) }
