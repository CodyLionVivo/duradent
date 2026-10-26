# Domain event bus: one SNS topic and, per consuming context, an SQS queue
# with a dead-letter queue and a filter policy on the "type" attribute.
# Mirrors infra/local/localstack/init-aws.sh.

variable "name" { type = string }
variable "consumers" {
  description = "Consumer name => event types it handles."
  type        = map(list(string))
}
variable "alarm_topic_arn" {
  description = "SNS topic for CloudWatch alarms (empty: no alarm actions)."
  type        = string
  default     = ""
}

resource "aws_sns_topic" "events" {
  name              = "${var.name}-domain-events"
  kms_master_key_id = "alias/aws/sns"
}

resource "aws_sqs_queue" "dlq" {
  for_each                  = var.consumers
  name                      = "${var.name}-${each.key}-dlq"
  message_retention_seconds = 1209600 # 14 days to inspect and redrive
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "queue" {
  for_each                   = var.consumers
  name                       = "${var.name}-${each.key}"
  visibility_timeout_seconds = 120 # longer than one Nubefact call with retries
  receive_wait_time_seconds  = 20
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq[each.key].arn
    maxReceiveCount     = 5
  })
}

data "aws_iam_policy_document" "queue" {
  for_each = var.consumers
  statement {
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.queue[each.key].arn]
    principals {
      type        = "Service"
      identifiers = ["sns.amazonaws.com"]
    }
    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_sns_topic.events.arn]
    }
  }
}

resource "aws_sqs_queue_policy" "queue" {
  for_each  = var.consumers
  queue_url = aws_sqs_queue.queue[each.key].id
  policy    = data.aws_iam_policy_document.queue[each.key].json
}

resource "aws_sns_topic_subscription" "queue" {
  for_each             = var.consumers
  topic_arn            = aws_sns_topic.events.arn
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.queue[each.key].arn
  raw_message_delivery = true
  filter_policy        = jsonencode({ type = each.value })
}

# Anything in a DLQ needs a person: alarm as soon as one message lands.
resource "aws_cloudwatch_metric_alarm" "dlq" {
  for_each            = var.consumers
  alarm_name          = "${var.name}-${each.key}-dlq-not-empty"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.dlq[each.key].name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = var.alarm_topic_arn == "" ? [] : [var.alarm_topic_arn]
}

output "topic_arn" { value = aws_sns_topic.events.arn }
output "queue_urls" { value = { for k, q in aws_sqs_queue.queue : k => q.id } }
output "queue_arns" { value = { for k, q in aws_sqs_queue.queue : k => q.arn } }
