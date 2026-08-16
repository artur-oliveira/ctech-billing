terraform {
  required_version = ">= 1.15"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.60"
    }
  }

  # One workspace, not three. The roles below are global: GitHub assumes the
  # same `ctech-billing-gha-infra` whether it is deploying dev or prod, and the
  # environment is decided by the branch inside the workflow. A per-environment
  # root would have three workspaces racing to own one role name.
  #
  # This is the same split ctech-cdk makes between its global OIDC stack and its
  # per-environment stacks.
  backend "s3" {
    bucket       = "prod-ctech-terraform-state"
    key          = "billing-github/terraform.tfstate"
    region       = "us-east-1"
    use_lockfile = true
    encrypt      = true
    profile      = "ctech"
  }
}

provider "aws" {
  region  = var.aws_region
  profile = "ctech"

  default_tags {
    tags = {
      Project   = "ctech-billing"
      ManagedBy = "terraform"
      Scope     = "global"
    }
  }
}
