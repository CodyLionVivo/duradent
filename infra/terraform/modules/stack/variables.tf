variable "env" {
  description = "staging | prod"
  type        = string
}

variable "domain" {
  description = "Public host of the store, e.g. duradent.pe or staging.duradent.pe."
  type        = string
}

variable "certificate_arn" {
  description = "ACM certificate (same region) for the store host, used by the ALB."
  type        = string
}

variable "mail_domain" {
  description = "Domain verified in SES for outgoing email (e.g. duradent.pe)."
  type        = string
}

variable "mail_from" {
  description = "From header of transactional email."
  type        = string
}

variable "cdn_aliases" {
  description = "Optional custom host for images (e.g. media.duradent.pe)."
  type        = list(string)
  default     = []
}

variable "cdn_certificate_arn" {
  description = "ACM certificate in us-east-1 for cdn_aliases."
  type        = string
  default     = ""
}

variable "api_repository_url" { type = string }
variable "web_repository_url" { type = string }
variable "image_tag" {
  description = "Image tag for the first apply; later deploys use infra/scripts/deploy.sh."
  type        = string
}

variable "single_nat_gateway" {
  type    = bool
  default = false
}

variable "db_instance_class" {
  type    = string
  default = "db.t4g.small"
}
variable "db_multi_az" {
  type    = bool
  default = true
}
variable "db_deletion_protection" {
  type    = bool
  default = true
}

variable "web_count" {
  type    = object({ min = number, max = number })
  default = { min = 2, max = 6 }
}
variable "api_count" {
  type    = object({ min = number, max = number })
  default = { min = 2, max = 6 }
}

variable "alarm_email" {
  description = "Receives CloudWatch alarms (confirm the subscription email)."
  type        = string
}

variable "admin_emails" { type = list(string) }
variable "ops_emails" {
  type    = list(string)
  default = []
}

variable "culqi_public_key" {
  type    = string
  default = ""
}
variable "google_client_id" {
  type    = string
  default = ""
}
variable "microsoft_client_id" {
  type    = string
  default = ""
}
variable "bank_details" { type = string }

variable "invoice_series" {
  type    = object({ factura = string, boleta = string, credit_f = string, credit_b = string })
  default = { factura = "F001", boleta = "B001", credit_f = "FC01", credit_b = "BC01" }
}

variable "log_retention_days" {
  type    = number
  default = 30
}

variable "create_mail_identity" {
  description = "Create the SES domain identity here (true in one environment per AWS account)."
  type        = bool
  default     = true
}
