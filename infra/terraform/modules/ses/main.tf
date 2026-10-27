# SES domain identity with Easy DKIM and a configuration set that sends
# bounces and complaints to CloudWatch. The DNS records are outputs: add them
# at the domain's DNS provider (or Route 53) and SES verifies the domain.
# New SES accounts start in the sandbox: request production access in the
# console before launch.

variable "name" { type = string }
variable "domain" { type = string }
variable "mail_from_subdomain" {
  type    = string
  default = "mail"
}
variable "create_identity" {
  description = "Create the domain identity (once per account; other environments reuse it)."
  type        = bool
  default     = true
}

resource "aws_sesv2_email_identity" "domain" {
  count                  = var.create_identity ? 1 : 0
  email_identity         = var.domain
  configuration_set_name = aws_sesv2_configuration_set.this.configuration_set_name
}

resource "aws_sesv2_email_identity_mail_from_attributes" "domain" {
  count                  = var.create_identity ? 1 : 0
  email_identity         = aws_sesv2_email_identity.domain[0].email_identity
  mail_from_domain       = "${var.mail_from_subdomain}.${var.domain}"
  behavior_on_mx_failure = "USE_DEFAULT_VALUE"
}

resource "aws_sesv2_configuration_set" "this" {
  configuration_set_name = var.name
  reputation_options {
    reputation_metrics_enabled = true
  }
  suppression_options {
    suppressed_reasons = ["BOUNCE", "COMPLAINT"]
  }
}

resource "aws_sesv2_configuration_set_event_destination" "cloudwatch" {
  configuration_set_name = aws_sesv2_configuration_set.this.configuration_set_name
  event_destination_name = "cloudwatch"
  event_destination {
    enabled              = true
    matching_event_types = ["SEND", "DELIVERY", "BOUNCE", "COMPLAINT", "REJECT"]
    cloud_watch_destination {
      dimension_configuration {
        default_dimension_value = "transactional"
        dimension_name          = "kind"
        dimension_value_source  = "MESSAGE_TAG"
      }
    }
  }
}

data "aws_region" "current" {}

output "configuration_set" { value = aws_sesv2_configuration_set.this.configuration_set_name }
output "dns_records" {
  description = "Records to publish for DKIM and the custom MAIL FROM domain."
  value = var.create_identity ? concat(
    [for t in aws_sesv2_email_identity.domain[0].dkim_signing_attributes[0].tokens : {
      type = "CNAME", name = "${t}._domainkey.${var.domain}", value = "${t}.dkim.amazonses.com"
    }],
    [
      { type = "MX", name = "${var.mail_from_subdomain}.${var.domain}", value = "10 feedback-smtp.${data.aws_region.current.region}.amazonses.com" },
      { type = "TXT", name = "${var.mail_from_subdomain}.${var.domain}", value = "v=spf1 include:amazonses.com ~all" },
      { type = "TXT", name = "_dmarc.${var.domain}", value = "v=DMARC1; p=quarantine; rua=mailto:dmarc@${var.domain}" },
    ],
  ) : []
}
