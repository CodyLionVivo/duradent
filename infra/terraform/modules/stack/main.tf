# One environment of the store: network, database, event bus, media CDN,
# email, the ECS cluster with its five task definitions (web, api, worker,
# outbox-relay, migrate) and the alarms.

terraform {
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 6.0" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

locals {
  name     = "duradent-${var.env}"
  web_url  = "https://${var.domain}"
  ca_file  = "/etc/ssl/rds-ca.pem" # shipped in the API image
  api_port = 8080
  web_port = 4321

  # Keep in sync with notifications/application.Events and invoicing/application.Events.
  consumers = {
    notifications = ["CustomerRegistered", "OrderPlaced", "PaymentConfirmed", "OrderShipped", "OrderCancelled",
    "OrderRefunded", "InvoiceIssued", "InvoiceRejected", "StockLow"]
    invoicing = ["PaymentConfirmed", "OrderRefunded"]
  }
}

data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

# --- alarms ------------------------------------------------------------------------

resource "aws_sns_topic" "alarms" {
  name = "${local.name}-alarms"
}

resource "aws_sns_topic_subscription" "alarm_email" {
  topic_arn = aws_sns_topic.alarms.arn
  protocol  = "email"
  endpoint  = var.alarm_email
}

# --- building blocks ------------------------------------------------------------------

module "network" {
  source             = "../network"
  name               = local.name
  single_nat_gateway = var.single_nat_gateway
}

module "alb" {
  source          = "../alb"
  name            = local.name
  vpc_id          = module.network.vpc_id
  subnet_ids      = module.network.public_subnet_ids
  certificate_arn = var.certificate_arn
  alarm_topic_arn = aws_sns_topic.alarms.arn
}

resource "aws_security_group" "tasks" {
  name        = "${local.name}-tasks"
  description = "ECS tasks: from the ALB and between services"
  vpc_id      = module.network.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "from_alb" {
  for_each                     = toset([tostring(local.web_port), tostring(local.api_port)])
  security_group_id            = aws_security_group.tasks.id
  referenced_security_group_id = module.alb.security_group_id
  ip_protocol                  = "tcp"
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
}

resource "aws_vpc_security_group_ingress_rule" "between_tasks" {
  security_group_id            = aws_security_group.tasks.id
  referenced_security_group_id = aws_security_group.tasks.id
  ip_protocol                  = "tcp"
  from_port                    = local.api_port
  to_port                      = local.api_port
}

resource "aws_vpc_security_group_egress_rule" "tasks" {
  security_group_id = aws_security_group.tasks.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1" # Culqi, Nubefact, Google/Microsoft OIDC, AWS APIs
}

module "db" {
  source                    = "../rds-mysql"
  name                      = local.name
  vpc_id                    = module.network.vpc_id
  subnet_ids                = module.network.private_subnet_ids
  client_security_group_ids = [aws_security_group.tasks.id]
  instance_class            = var.db_instance_class
  multi_az                  = var.db_multi_az
  deletion_protection       = var.db_deletion_protection
  alarm_topic_arn           = aws_sns_topic.alarms.arn
}

module "messaging" {
  source          = "../messaging"
  name            = local.name
  consumers       = local.consumers
  alarm_topic_arn = aws_sns_topic.alarms.arn
}

module "media" {
  source          = "../s3-cdn"
  name            = local.name
  bucket_name     = "${local.name}-media-${data.aws_caller_identity.current.account_id}"
  upload_origins  = [local.web_url]
  aliases         = var.cdn_aliases
  certificate_arn = var.cdn_certificate_arn
}

module "ses" {
  source          = "../ses"
  name            = local.name
  domain          = var.mail_domain
  create_identity = var.create_mail_identity
}

# --- secrets --------------------------------------------------------------------------

# Third-party credentials, one JSON secret per environment. Terraform creates
# the container; operations fills the values (see the runbook in CLAUDE.md):
# {"CULQI_SECRET_KEY": "...", "CULQI_WEBHOOK_SECRET": "...", "NUBEFACT_URL": "...",
#  "NUBEFACT_TOKEN": "...", "GOOGLE_CLIENT_SECRET": "", "MICROSOFT_CLIENT_SECRET": "", "ADMIN_TOKEN": ""}
resource "aws_secretsmanager_secret" "app" {
  name                    = "${local.name}/app"
  description             = "Culqi, Nubefact, OIDC and admin token for duradent ${var.env}"
  recovery_window_in_days = 7
}

locals {
  app_secret_keys = ["CULQI_SECRET_KEY", "CULQI_WEBHOOK_SECRET", "NUBEFACT_URL", "NUBEFACT_TOKEN",
  "GOOGLE_CLIENT_SECRET", "MICROSOFT_CLIENT_SECRET", "ADMIN_TOKEN"]
  api_secrets = merge(
    { MYSQL_DSN = module.db.dsn_secret_arn },
    { for k in local.app_secret_keys : k => "${aws_secretsmanager_secret.app.arn}:${k}::" },
  )
  api_env = {
    APP_ENV                 = var.env
    LOG_LEVEL               = "info"
    AWS_REGION              = data.aws_region.current.region
    MYSQL_CA_FILE           = local.ca_file
    WEB_BASE_URL            = local.web_url
    S3_MEDIA_BUCKET         = module.media.bucket_name
    MEDIA_BASE_URL          = module.media.base_url
    TRUST_PROXY_HEADERS     = "true"
    ADMIN_EMAILS            = join(",", var.admin_emails)
    OPS_EMAILS              = join(",", length(var.ops_emails) > 0 ? var.ops_emails : var.admin_emails)
    GOOGLE_CLIENT_ID        = var.google_client_id
    MICROSOFT_CLIENT_ID     = var.microsoft_client_id
    CULQI_PUBLIC_KEY        = var.culqi_public_key
    BANK_DETAILS            = var.bank_details
    SNS_TOPIC_ARN           = module.messaging.topic_arn
    SQS_NOTIFICATIONS_URL   = module.messaging.queue_urls["notifications"]
    SQS_INVOICING_URL       = module.messaging.queue_urls["invoicing"]
    MAIL_FROM               = var.mail_from
    SES_CONFIGURATION_SET   = module.ses.configuration_set
    INVOICE_SERIES_FACTURA  = var.invoice_series.factura
    INVOICE_SERIES_BOLETA   = var.invoice_series.boleta
    INVOICE_SERIES_CREDIT_F = var.invoice_series.credit_f
    INVOICE_SERIES_CREDIT_B = var.invoice_series.credit_b
  }
}

# --- IAM ------------------------------------------------------------------------------

data "aws_iam_policy_document" "ecs_tasks_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "execution" {
  name               = "${local.name}-ecs-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume.json
}

resource "aws_iam_role_policy_attachment" "execution" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "execution_secrets" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.app.arn, module.db.dsn_secret_arn]
  }
}

resource "aws_iam_role_policy" "execution_secrets" {
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution_secrets.json
}

# Per-service task roles: each process gets only what it calls.
locals {
  exec_statement = {
    actions   = ["ssmmessages:CreateControlChannel", "ssmmessages:CreateDataChannel", "ssmmessages:OpenControlChannel", "ssmmessages:OpenDataChannel"]
    resources = ["*"]
  }
  ses_statement = {
    actions   = ["ses:SendEmail", "ses:SendRawEmail"]
    resources = ["*"] # SES authorises by identity; the from address must be verified
  }
  task_policies = {
    web = [local.exec_statement]
    api = [local.exec_statement, local.ses_statement, {
      actions   = ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"]
      resources = ["${module.media.bucket_arn}/*"]
    }]
    worker = [local.exec_statement, local.ses_statement, {
      actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes"]
      resources = values(module.messaging.queue_arns)
    }]
    relay = [local.exec_statement, {
      actions   = ["sns:Publish"]
      resources = [module.messaging.topic_arn]
    }]
    migrate = [local.exec_statement]
  }
}

resource "aws_iam_role" "task" {
  for_each           = local.task_policies
  name               = "${local.name}-${each.key}"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume.json
}

data "aws_iam_policy_document" "task" {
  for_each = local.task_policies
  dynamic "statement" {
    for_each = each.value
    content {
      actions   = statement.value.actions
      resources = statement.value.resources
    }
  }
}

resource "aws_iam_role_policy" "task" {
  for_each = local.task_policies
  role     = aws_iam_role.task[each.key].id
  policy   = data.aws_iam_policy_document.task[each.key].json
}

# --- ECS ------------------------------------------------------------------------------

resource "aws_service_discovery_http_namespace" "this" {
  name = "${local.name}.internal"
}

resource "aws_ecs_cluster" "this" {
  name = local.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
  service_connect_defaults {
    namespace = aws_service_discovery_http_namespace.this.arn
  }
}

locals {
  api_image = "${var.api_repository_url}:${var.image_tag}"
  common = {
    cluster_arn               = aws_ecs_cluster.this.arn
    execution_role_arn        = aws_iam_role.execution.arn
    subnet_ids                = module.network.private_subnet_ids
    security_group_ids        = [aws_security_group.tasks.id]
    service_connect_namespace = aws_service_discovery_http_namespace.this.arn
    log_retention_days        = var.log_retention_days
  }
}

module "api" {
  source                    = "../ecs-service"
  name                      = "${local.name}-api"
  image                     = local.api_image
  command                   = ["api"]
  cpu                       = 512
  memory                    = 1024
  environment               = local.api_env
  secrets                   = local.api_secrets
  task_role_arn             = aws_iam_role.task["api"].arn
  desired_count             = var.api_count.min
  max_count                 = var.api_count.max
  target_group_arn          = module.alb.api_target_group_arn
  service_connect_name      = "api"
  cluster_arn               = local.common.cluster_arn
  execution_role_arn        = local.common.execution_role_arn
  subnet_ids                = local.common.subnet_ids
  security_group_ids        = local.common.security_group_ids
  service_connect_namespace = local.common.service_connect_namespace
  log_retention_days        = local.common.log_retention_days
}

module "web" {
  source         = "../ecs-service"
  name           = "${local.name}-web"
  image          = "${var.web_repository_url}:${var.image_tag}"
  container_port = local.web_port
  cpu            = 512
  memory         = 1024
  environment = {
    API_URL     = "http://api:${local.api_port}" # Service Connect
    TRUST_PROXY = "true"
  }
  task_role_arn             = aws_iam_role.task["web"].arn
  desired_count             = var.web_count.min
  max_count                 = var.web_count.max
  target_group_arn          = module.alb.web_target_group_arn
  cluster_arn               = local.common.cluster_arn
  execution_role_arn        = local.common.execution_role_arn
  subnet_ids                = local.common.subnet_ids
  security_group_ids        = local.common.security_group_ids
  service_connect_namespace = local.common.service_connect_namespace
  log_retention_days        = local.common.log_retention_days
  depends_on                = [module.api]
}

module "worker" {
  source                    = "../ecs-service"
  name                      = "${local.name}-worker"
  image                     = local.api_image
  command                   = ["worker"]
  environment               = local.api_env
  secrets                   = local.api_secrets
  task_role_arn             = aws_iam_role.task["worker"].arn
  desired_count             = 1
  max_count                 = 1
  cluster_arn               = local.common.cluster_arn
  execution_role_arn        = local.common.execution_role_arn
  subnet_ids                = local.common.subnet_ids
  security_group_ids        = local.common.security_group_ids
  service_connect_namespace = local.common.service_connect_namespace
  log_retention_days        = local.common.log_retention_days
}

module "relay" {
  source                    = "../ecs-service"
  name                      = "${local.name}-outbox-relay"
  image                     = local.api_image
  command                   = ["outbox-relay"]
  environment               = local.api_env
  secrets                   = local.api_secrets
  task_role_arn             = aws_iam_role.task["relay"].arn
  desired_count             = 1
  max_count                 = 1
  cluster_arn               = local.common.cluster_arn
  execution_role_arn        = local.common.execution_role_arn
  subnet_ids                = local.common.subnet_ids
  security_group_ids        = local.common.security_group_ids
  service_connect_namespace = local.common.service_connect_namespace
  log_retention_days        = local.common.log_retention_days
}

# Run once per deploy, before the services roll (infra/scripts/deploy.sh).
module "migrate" {
  source             = "../ecs-service"
  name               = "${local.name}-migrate"
  image              = local.api_image
  command            = ["migrate"]
  environment        = local.api_env
  secrets            = local.api_secrets
  task_role_arn      = aws_iam_role.task["migrate"].arn
  desired_count      = null
  cluster_arn        = local.common.cluster_arn
  execution_role_arn = local.common.execution_role_arn
  subnet_ids         = local.common.subnet_ids
  security_group_ids = local.common.security_group_ids
  log_retention_days = local.common.log_retention_days
}

# Errors logged by any Go process (slog JSON level ERROR).
resource "aws_cloudwatch_log_metric_filter" "errors" {
  for_each       = toset(["api", "worker", "outbox-relay"])
  name           = "${local.name}-${each.key}-errors"
  log_group_name = "/ecs/${local.name}-${each.key}"
  pattern        = "{ $.level = \"ERROR\" }"
  metric_transformation {
    name      = "${each.key}-errors"
    namespace = "Duradent/${var.env}"
    value     = "1"
  }
  depends_on = [module.api, module.worker, module.relay]
}

resource "aws_cloudwatch_metric_alarm" "errors" {
  for_each            = aws_cloudwatch_log_metric_filter.errors
  alarm_name          = each.value.name
  namespace           = "Duradent/${var.env}"
  metric_name         = each.value.metric_transformation[0].name
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 5
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}
