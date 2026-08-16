# The service's compute: one ASG of private-IPv4-only instances behind the
# shared HAProxy edge, running the API and — on the leader — the two scheduled
# jobs.
#
# Port of ctech-wallet's ApiStack + @aoctech/cdk's HaproxyEc2Service. Terraform
# rather than CDK because ADR 0010 already decided that for this repository; the
# shape is deliberately identical so an operator who has debugged one service
# has debugged this one.

data "aws_ssm_parameter" "vpc_id" {
  name = local.shared_ssm.vpc_id
}

data "aws_vpc" "this" {
  id = data.aws_ssm_parameter.vpc_id.value
}

data "aws_subnets" "public" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.this.id]
  }
  # ctech-cdk's NetworkStack tags subnets with the standard CDK marker. Matching
  # on that rather than on Name is what keeps this working when a subnet is
  # renamed.
  tags = {
    "aws-cdk:subnet-type" = "Public"
  }
}

data "aws_ssm_parameter" "edge_security_group_id" {
  name = local.shared_ssm.edge_security_group_id
}

data "aws_ssm_parameter" "deployments_bucket" {
  name = local.shared_ssm.deployments_bucket
}

data "aws_ssm_parameter" "logs_bucket" {
  name = local.shared_ssm.logs_bucket
}

data "aws_ssm_parameter" "al2023_arm64_ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-minimal-kernel-default-arm64"
}

# Egress is open; ingress is only the edge. These instances have no public IPv4,
# so "open egress" means IPv6 and the VPC — there is no inbound path that does
# not come through HAProxy.
resource "aws_security_group" "instances" {
  name        = "${local.name}-api-sg"
  description = "ctech-billing API instances"
  vpc_id      = data.aws_vpc.this.id

  ingress {
    description     = "CTech HAProxy edge to service"
    from_port       = local.nginx_port
    to_port         = local.nginx_port
    protocol        = "tcp"
    security_groups = [data.aws_ssm_parameter.edge_security_group_id.value]
  }

  egress {
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    cidr_blocks      = ["0.0.0.0/0"]
    ipv6_cidr_blocks = ["::/0"]
  }

  lifecycle {
    create_before_destroy = true
  }
}

locals {
  bootstrap_sh = templatefile("${path.module}/../assets/bootstrap.sh.tftpl", {
    aws_region  = var.aws_region
    environment = var.environment

    app_port     = local.app_port
    nginx_port   = local.nginx_port
    health_path  = local.health_path
    table_prefix = local.table_prefix
    vpc_cidr     = data.aws_vpc.this.cidr_block
    asg_name     = local.asg_name

    deployments_bucket   = data.aws_ssm_parameter.deployments_bucket.value
    logs_bucket          = data.aws_ssm_parameter.logs_bucket.value
    s3_prefix            = local.s3_prefix
    current_artifact_key = local.current_artifact_key

    log_group_app         = local.log_group_app
    log_group_nginx       = local.log_group_nginx
    host_metric_namespace = "${local.metric_namespace}/Host"

    service_audience       = local.app_domain_url
    checkout_base_url      = "${local.app_domain_url}/checkout"
    portal_organization_id = var.portal_organization_id
    cors_allowed_origins   = join(",", local.cors_allowed_origins)

    valkey_db                = local.valkey_db
    ssm_valkey_url           = local.shared_ssm.valkey_url
    ssm_account_internal_url = local.account_ssm.internal_base_url
    ssm_account_app_url      = local.account_ssm.app_url
    ssm_account_jwks_url     = local.account_ssm.internal_jwks_url
    ssm_wallet_internal_url  = local.wallet_ssm.internal_base_url

    ssm_wallet_client_id      = local.ssm_paths.wallet_client_id
    ssm_wallet_client_secret  = local.ssm_paths.wallet_client_secret
    ssm_wallet_webhook_secret = local.ssm_paths.wallet_webhook_secret
    ssm_checkout_link_secret  = local.ssm_paths.checkout_link_secret
    ssm_field_encryption_key  = local.ssm_paths.field_encryption_key
    ssm_email_from            = local.ssm_paths.email_from
  })

  # gzip + base64 keeps the payload under EC2's 16 KiB user_data limit — the
  # bootstrap script alone is well past it uncompressed.
  user_data = base64encode(<<-EOF
    #!/bin/bash
    set -euxo pipefail
    mkdir -p /opt/app
    echo '${base64gzip(local.bootstrap_sh)}' | base64 -d | gzip -d > /opt/app/bootstrap.sh
    chmod 0750 /opt/app/bootstrap.sh
    /opt/app/bootstrap.sh
    EOF
  )
}

resource "aws_launch_template" "this" {
  name          = "${local.asg_name}-lt"
  instance_type = var.instance_type
  image_id      = data.aws_ssm_parameter.al2023_arm64_ami.value

  # T4g has no launch credits; "standard" matches ec2.CpuCredits.STANDARD.
  credit_specification {
    cpu_credits = "standard"
  }

  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      volume_size           = 4
      volume_type           = "gp3"
      delete_on_termination = true
      encrypted             = true
    }
  }

  iam_instance_profile {
    name = aws_iam_instance_profile.billing.name
  }

  metadata_options {
    http_tokens = "required"
  }

  # The security group goes here rather than at the top level:
  # associate_public_ip_address and ipv6_address_count can only be set on a
  # network interface, and AWS rejects a template that sets both forms at once.
  network_interfaces {
    device_index                = 0
    associate_public_ip_address = false
    ipv6_address_count          = 1
    security_groups             = [aws_security_group.instances.id]
  }

  user_data = local.user_data

  tag_specifications {
    resource_type = "instance"
    tags          = { Name = local.asg_name }
  }

  tag_specifications {
    resource_type = "volume"
    tags          = { Name = local.asg_name }
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_autoscaling_group" "this" {
  name                = local.asg_name
  vpc_zone_identifier = data.aws_subnets.public.ids
  min_size            = var.min_size
  max_size            = var.max_size
  default_cooldown    = 120

  # EC2 health, not ELB: there is no target group. HAProxy owns the application
  # probe and, with autoHeal on the route, reports repeated failures back
  # through SetInstanceHealth — so the ASG still replaces an instance the app
  # has died on, it just learns about it from the thing actually serving
  # traffic.
  health_check_type         = "EC2"
  health_check_grace_period = 180

  launch_template {
    id      = aws_launch_template.this.id
    version = aws_launch_template.this.latest_version
  }

  # An instance is only useful once bootstrap.sh has finished and the first
  # deploy has landed; without this, a rolling replacement can take the last
  # healthy instance out while the new one is still installing nginx.
  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 100
      instance_warmup        = 300
    }
  }

  dynamic "tag" {
    for_each = {
      Name        = local.asg_name
      Environment = var.environment
      Project     = "ctech-billing"
    }
    content {
      key                 = tag.key
      value               = tag.value
      propagate_at_launch = true
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}
