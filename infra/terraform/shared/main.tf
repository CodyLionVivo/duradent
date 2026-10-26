# Account-wide resources shared by staging and prod: the image repositories
# and the role GitHub Actions assumes (OIDC, no stored AWS keys).

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
  backend "s3" {} # terraform init -backend-config=../backend.hcl -backend-config="key=shared.tfstate"
}

variable "region" {
  type    = string
  default = "us-east-1"
}
variable "github_repository" {
  description = "owner/name allowed to deploy, e.g. CodyLionVivo/duradent."
  type        = string
}
variable "state_bucket" {
  description = "Bucket holding the Terraform state (the deploy role reads env outputs)."
  type        = string
}
variable "lock_table" {
  type = string
}

provider "aws" {
  region = var.region
  default_tags {
    tags = { Project = "duradent", ManagedBy = "terraform" }
  }
}

data "aws_caller_identity" "current" {}

resource "aws_ecr_repository" "app" {
  for_each             = toset(["api", "web"])
  name                 = "duradent/${each.key}"
  image_tag_mutability = "IMMUTABLE" # a tag always means the same build (rollbacks by tag)
  image_scanning_configuration {
    scan_on_push = true
  }
  encryption_configuration {
    encryption_type = "AES256"
  }
}

resource "aws_ecr_lifecycle_policy" "app" {
  for_each   = aws_ecr_repository.app
  repository = each.value.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the last 50 images"
      selection    = { tagStatus = "any", countType = "imageCountMoreThan", countNumber = 50 }
      action       = { type = "expire" }
    }]
  })
}

resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

data "aws_iam_policy_document" "github_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    # Only main and the protected deploy environments can assume the role.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values = [
        "repo:${var.github_repository}:ref:refs/heads/main",
        "repo:${var.github_repository}:environment:staging",
        "repo:${var.github_repository}:environment:prod",
      ]
    }
  }
}

resource "aws_iam_role" "deploy" {
  name               = "duradent-github-deploy"
  assume_role_policy = data.aws_iam_policy_document.github_assume.json
}

data "aws_iam_policy_document" "deploy" {
  statement {
    sid       = "PushImages"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    actions = ["ecr:BatchCheckLayerAvailability", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart",
    "ecr:CompleteLayerUpload", "ecr:PutImage", "ecr:BatchGetImage", "ecr:DescribeImages"]
    resources = [for r in aws_ecr_repository.app : r.arn]
  }
  statement {
    sid = "RollOutServices"
    actions = ["ecs:DescribeTaskDefinition", "ecs:RegisterTaskDefinition", "ecs:RunTask", "ecs:DescribeTasks",
    "ecs:UpdateService", "ecs:DescribeServices"]
    resources = ["*"]
  }
  statement {
    actions   = ["iam:PassRole"]
    resources = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/duradent-*"]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
  statement {
    sid       = "ReadTerraformOutputs"
    actions   = ["s3:GetObject", "s3:ListBucket"]
    resources = ["arn:aws:s3:::${var.state_bucket}", "arn:aws:s3:::${var.state_bucket}/*"]
  }
  statement {
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    resources = ["arn:aws:dynamodb:${var.region}:${data.aws_caller_identity.current.account_id}:table/${var.lock_table}"]
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:GetLogEvents", "logs:FilterLogEvents"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "deploy" {
  role   = aws_iam_role.deploy.id
  policy = data.aws_iam_policy_document.deploy.json
}

output "api_repository_url" { value = aws_ecr_repository.app["api"].repository_url }
output "web_repository_url" { value = aws_ecr_repository.app["web"].repository_url }
output "deploy_role_arn" { value = aws_iam_role.deploy.arn }

# Pull requests run `terraform plan` with a read-only role.
data "aws_iam_policy_document" "github_plan_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:pull_request"]
    }
  }
}

resource "aws_iam_role" "plan" {
  name               = "duradent-github-plan"
  assume_role_policy = data.aws_iam_policy_document.github_plan_assume.json
}

resource "aws_iam_role_policy_attachment" "plan_readonly" {
  role       = aws_iam_role.plan.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

data "aws_iam_policy_document" "plan" {
  statement {
    sid       = "RefreshGeneratedSecrets" # the DSN secret version Terraform manages
    actions   = ["secretsmanager:GetSecretValue"]
    resources = ["arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:duradent-*/mysql-dsn-*"]
  }
  statement {
    sid       = "StateLock"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    resources = ["arn:aws:dynamodb:${var.region}:${data.aws_caller_identity.current.account_id}:table/${var.lock_table}"]
  }
}

resource "aws_iam_role_policy" "plan" {
  role   = aws_iam_role.plan.id
  policy = data.aws_iam_policy_document.plan.json
}

output "plan_role_arn" { value = aws_iam_role.plan.arn }
