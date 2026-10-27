#!/bin/bash
# Runs inside LocalStack once it is ready (mounted into /etc/localstack/init/ready.d).
# Mirrors what Terraform creates in AWS: one SNS topic for domain events and one
# SQS queue + DLQ per consuming bounded context, plus the media bucket.
set -euo pipefail

REGION="${AWS_DEFAULT_REGION:-us-east-1}"
TOPIC_ARN=$(awslocal sns create-topic --name duradent-domain-events --query TopicArn --output text)

# consumer → event types it handles (SNS filter policy on the "type" attribute).
# Keep in sync with notifications/application.Events and invoicing/application.Events.
declare -A FILTERS=(
  [notifications]='["CustomerRegistered","OrderPlaced","PaymentConfirmed","OrderShipped","OrderCancelled","OrderRefunded","InvoiceIssued","InvoiceRejected","StockLow","ComplaintFiled","ComplaintAnswered"]'
  [invoicing]='["PaymentConfirmed","OrderRefunded"]'
)

for consumer in "${!FILTERS[@]}"; do
  dlq_url=$(awslocal sqs create-queue --queue-name "duradent-${consumer}-dlq" --query QueueUrl --output text)
  dlq_arn=$(awslocal sqs get-queue-attributes --queue-url "$dlq_url" --attribute-names QueueArn --query Attributes.QueueArn --output text)

  queue_url=$(awslocal sqs create-queue --queue-name "duradent-${consumer}" \
    --attributes "{\"VisibilityTimeout\":\"60\",\"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"${dlq_arn}\\\",\\\"maxReceiveCount\\\":\\\"5\\\"}\"}" \
    --query QueueUrl --output text)
  queue_arn=$(awslocal sqs get-queue-attributes --queue-url "$queue_url" --attribute-names QueueArn --query Attributes.QueueArn --output text)

  # Raw delivery: consumers receive the event envelope, not the SNS wrapper.
  sub_arn=$(awslocal sns subscribe --topic-arn "$TOPIC_ARN" --protocol sqs --notification-endpoint "$queue_arn" \
    --attributes RawMessageDelivery=true --query SubscriptionArn --output text)
  awslocal sns set-subscription-attributes --subscription-arn "$sub_arn" \
    --attribute-name FilterPolicy --attribute-value "{\"type\":${FILTERS[$consumer]}}"
done

BUCKET=duradent-media-local
awslocal s3 mb "s3://${BUCKET}" >/dev/null || true
# The admin panel uploads images straight from the browser (presigned PUT).
awslocal s3api put-bucket-cors --bucket "$BUCKET" --cors-configuration '{
  "CORSRules": [{"AllowedOrigins": ["http://localhost:4321"], "AllowedMethods": ["PUT", "GET"],
                 "AllowedHeaders": ["*"], "MaxAgeSeconds": 3000}]}'
# Product images are public (in AWS they are served through CloudFront instead).
awslocal s3api put-bucket-policy --bucket "$BUCKET" --policy "{
  \"Version\": \"2012-10-17\",
  \"Statement\": [{\"Effect\": \"Allow\", \"Principal\": \"*\", \"Action\": \"s3:GetObject\",
                   \"Resource\": \"arn:aws:s3:::${BUCKET}/*\"}]}"

touch /tmp/duradent-ready
echo "duradent: localstack resources ready in ${REGION}"
