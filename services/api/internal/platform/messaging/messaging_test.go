package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/testcontainers/testcontainers-go"
	tclocalstack "github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/messaging"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/outbox"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

func awsConfig(t *testing.T) aws.Config {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := tclocalstack.Run(ctx, "localstack/localstack:4.9", testcontainers.WithEnv(map[string]string{"SERVICES": "sns,sqs"}))
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "4566/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return aws.Config{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		BaseEndpoint: aws.String("http://" + host + ":" + port.Port()),
	}
}

func TestOutboxToSNSToSQSConsumer(t *testing.T) {
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	cfg := awsConfig(t)
	ctx := context.Background()
	snsc, sqsc := sns.NewFromConfig(cfg), sqs.NewFromConfig(cfg)

	topic, err := snsc.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("events")})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := sqsc.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("notifications"), Attributes: map[string]string{"VisibilityTimeout": "1"}})
	attrs, _ := sqsc.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	_, err = snsc.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: topic.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(attrs.Attributes["QueueArn"]),
		Attributes: map[string]string{"RawMessageDelivery": "true", "FilterPolicy": `{"type":["OrderPlaced"]}`},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two events in the outbox; only OrderPlaced matches the filter policy.
	w := outbox.NewWriter(db)
	orderID := id.New()
	_ = w.Record(ctx, "order", orderID, "OrderPlaced", map[string]string{"number": "DD-000001"}, time.Now().UTC())
	_ = w.Record(ctx, "variant", id.New(), "StockLow", map[string]int{"available": 2}, time.Now().UTC())

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := outbox.NewRelay(db, messaging.NewSNSPublisher(snsc, aws.ToString(topic.TopicArn)), log)
	if n, err := relay.PublishBatch(ctx); err != nil || n != 2 {
		t.Fatalf("relay: %d %v", n, err)
	}
	if n, _ := relay.PublishBatch(ctx); n != 0 {
		t.Fatal("published rows are not published again")
	}

	var mu sync.Mutex
	var seen []outbox.Event
	fail := true
	handler := func(_ context.Context, e outbox.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			fail = false
			return errors.New("transient failure")
		}
		seen = append(seen, e)
		return nil
	}
	consumer := messaging.NewConsumer("notifications", sqsc, aws.ToString(q.QueueUrl), db, handler, log)
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { consumer.Run(cctx); close(done) }()

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(seen)
		mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	<-done

	if len(seen) != 1 || seen[0].Type != "OrderPlaced" || seen[0].AggregateID != orderID {
		t.Fatalf("consumer saw %+v", seen)
	}
	var payload map[string]string
	_ = json.Unmarshal(seen[0].Payload, &payload)
	if payload["number"] != "DD-000001" {
		t.Fatalf("payload: %s", seen[0].Payload)
	}

	// A redelivered copy of the same event is skipped.
	body, _ := json.Marshal(seen[0])
	if err := consumer.Process(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatal("duplicate delivery must not run the handler again")
	}
	if err := consumer.Process(ctx, "not json"); err == nil {
		t.Fatal("garbage stays in the queue (and ends in the DLQ)")
	}
}

func TestRelayBacksOffOnPublishErrors(t *testing.T) {
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = outbox.NewWriter(db).Record(ctx, "order", id.New(), "OrderPlaced", map[string]string{}, time.Now().UTC())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := outbox.NewRelay(db, failing{}, log)
	if n, _ := relay.PublishBatch(ctx); n != 1 {
		t.Fatal("first attempt takes the row")
	}
	if n, _ := relay.PublishBatch(ctx); n != 0 {
		t.Fatal("a failed row waits for its backoff")
	}
	var attempts int
	var lastErr string
	_ = db.QueryRowContext(ctx, "SELECT attempts, last_error FROM outbox").Scan(&attempts, &lastErr)
	if attempts != 1 || lastErr != "bus down" {
		t.Fatalf("attempts %d err %q", attempts, lastErr)
	}
}

type failing struct{}

func (failing) Publish(context.Context, outbox.Event) error { return errors.New("bus down") }
