terraform {
  required_version = ">= 1.15"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.60"
    }
  }

  # One Terraform workspace per environment (dev/stage/prod). The S3 backend
  # nests non-defaul workspaces under "env:/<workspace>/<key>" automatically.
  # The bucket and the lock table are the shared ones created by
  # ctech-lbalancer/scripts/bootstrap-terraform-state.sh — this root does not
  # create its own state infrastructure, it joins the existing one.
  backend "s3" {
    bucket       = "prod-ctech-terraform-state"
    key          = "billing/terraform.tfstate"
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
      Environment = var.environment
      Project     = "ctech-billing"
      ManagedBy   = "terraform"
    }
  }
}
