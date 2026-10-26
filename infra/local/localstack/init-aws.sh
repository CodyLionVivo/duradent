#!/bin/bash
# Runs inside LocalStack once it is ready (mounted into /etc/localstack/init/ready.d).
# Mirrors what Terraform creates in AWS: one SNS topic for domain events and one
# SQS queue + DLQ per consuming bounded context, plus the media bucket.
set -euo pipefail

REGION="${AWS_DEFAULT_REGION:-us-east-1}"
TOPIC_ARN=$(awslocal sns create-topic --name duradent-domain-events --query TopicArn --output text)

for consumer in orders inventory payments invoicing notifications; do
  dlq_url=$(awslocal sqs create-queue --queue-name "duradent-${consumer}-dlq" --query QueueUrl --output text)
  dlq_arn=$(awslocal sqs get-queue-attributes --queue-url "$dlq_url" --attribute-names QueueArn --query Attributes.QueueArn --output text)

  queue_url=$(awslocal sqs create-queue --queue-name "duradent-${consumer}" \
    --attributes "{\"VisibilityTimeout\":\"60\",\"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"${dlq_arn}\\\",\\\"maxReceiveCount\\\":\\\"5\\\"}\"}" \
    --query QueueUrl --output text)
  queue_arn=$(awslocal sqs get-queue-attributes --queue-url "$queue_url" --attribute-names QueueArn --query Attributes.QueueArn --output text)

  # Raw delivery: consumers receive the event envelope, not the SNS wrapper.
  awslocal sns subscribe --topic-arn "$TOPIC_ARN" --protocol sqs --notification-endpoint "$queue_arn" \
    --attributes RawMessageDelivery=true >/dev/null
done

awslocal s3 mb s3://duradent-media-local >/dev/null || true

echo "duradent: localstack resources ready in ${REGION}"
