terraform {
  required_version = ">= 1.6"
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 6.0" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
  backend "s3" {} # terraform init -backend-config=../../backend.hcl -backend-config="key=prod.tfstate"
}

provider "aws" {
  region = var.region
  default_tags {
    tags = { Project = "duradent", Environment = "prod", ManagedBy = "terraform" }
  }
}

variable "region" {
  type    = string
  default = "us-east-1"
}
variable "image_tag" { type = string }
variable "certificate_arn" { type = string }
variable "api_repository_url" { type = string }
variable "web_repository_url" { type = string }
variable "alarm_email" { type = string }
variable "admin_emails" { type = list(string) }
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

# Production: NAT gateway and database replica per AZ, two tasks minimum.
module "stack" {
  source                 = "../../modules/stack"
  env                    = "prod"
  domain                 = "duradent.pe"
  mail_domain            = "duradent.pe"
  mail_from              = "Duradent <no-reply@duradent.pe>"
  certificate_arn        = var.certificate_arn
  api_repository_url     = var.api_repository_url
  web_repository_url     = var.web_repository_url
  image_tag              = var.image_tag
  single_nat_gateway     = false
  db_instance_class      = "db.t4g.small"
  db_multi_az            = true
  db_deletion_protection = true
  web_count              = { min = 2, max = 6 }
  api_count              = { min = 2, max = 6 }
  alarm_email            = var.alarm_email
  admin_emails           = var.admin_emails
  culqi_public_key       = var.culqi_public_key
  google_client_id       = var.google_client_id
  microsoft_client_id    = var.microsoft_client_id
  bank_details           = var.bank_details
  log_retention_days     = 90
}

output "alb_dns_name" { value = module.stack.alb_dns_name }
output "media_base_url" { value = module.stack.media_base_url }
output "ses_dns_records" { value = module.stack.ses_dns_records }
output "app_secret_arn" { value = module.stack.app_secret_arn }
output "culqi_webhook_url_prefix" { value = module.stack.culqi_webhook_url_prefix }
output "deploy" { value = module.stack.deploy }
