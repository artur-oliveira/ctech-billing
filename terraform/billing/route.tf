# The HAProxy route, owned here rather than in ctech-lbalancer.
#
# ctech-lbalancer's `default_routes` are its bootstrap set, gated behind
# `manage_routes`; the edge itself reads every parameter under
# /ctech/{env}/lbalancer/routes/ and does not care who wrote them. Registering
# from this root is what @aoctech/cdk's HaproxyEc2Service does for every other
# service (its `route` prop), and it keeps the hostname, the ASG name, the port
# and the health path in the same file as the ASG they describe. A route living
# in another repository is a route that outlives the thing it points at.

resource "aws_ssm_parameter" "route" {
  name        = "${local.shared_ssm.routes}/billing"
  type        = "String"
  tier        = "Standard"
  description = "HAProxy route for ${local.api_domain}"

  value = jsonencode({
    hostname         = local.api_domain
    internalHostname = local.internal_domain
    asg              = aws_autoscaling_group.this.name
    port             = local.nginx_port
    healthPath       = local.health_path
    healthyStatuses  = [200]
    # HAProxy reports repeated probe failures back through SetInstanceHealth.
    # That is the entire reason the ASG can use EC2 health checks and still
    # replace an instance whose app has died.
    autoHeal = true
  })
}

# The private name wallet uses for the notify-back, and the one billing's own
# jobs would use. The public hostname would work and would be wrong: a webhook
# carrying an HMAC signature has no reason to leave the VPC.
data "aws_ssm_parameter" "private_hosted_zone_id" {
  name = local.private_hosted_zone_id_parameter
}

resource "aws_route53_record" "internal_alias" {
  zone_id = data.aws_ssm_parameter.private_hosted_zone_id.value
  name    = local.internal_domain
  type    = "CNAME"
  ttl     = 30
  records = [local.internal_lbalancer_domain]
}
