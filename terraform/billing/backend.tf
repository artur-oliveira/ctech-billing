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
  # No `profile` here, and none on the provider below. Both roots take
  # credentials from the standard AWS chain, which means `AWS_PROFILE=ctech` on a
  # workstation and the assumed-role environment variables in CI — see
  # ../README.md.
  #
  # It used to say `profile = "ctech"`, which a backend block cannot express as a
  # variable, so CI compensated by writing a `[profile ctech]` into ~/.aws/config
  # with `credential_source = Environment`. That is not a valid profile: the
  # setting means "use these credentials to assume role_arn", and there was no
  # role_arn to assume, so `terraform init` failed with
  # "credential type credential_source requires role_arn, profile ctech".
  # Naming the profile in source is what made the shim necessary at all.
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
