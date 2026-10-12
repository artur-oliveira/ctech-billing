# The Alpine root is 1 GB (owner decision 2026-10-12, no bigger volume): the
# swap file and two releases side by side must fit in it. 128 MB of swap leaves
# room to unpack the next release beside the current one; 256 MB did not
# (the 2026-10-12 deploy failed with "disk full").
mock_provider "aws" {
  mock_data "aws_ssm_parameter" {
    defaults = { value = "mock" }
  }
  mock_data "aws_vpc" {
    defaults = { cidr_block = "10.0.0.0/16" }
  }
  mock_data "aws_iam_policy_document" {
    defaults = { json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}" }
  }
}
mock_provider "archive" {}

variables {
  environment = "prod"
  os_family   = "alpine"
}

run "alpine_swap_leaves_room_for_two_releases" {
  command = plan

  assert {
    condition     = strcontains(local.bootstrap_sh, "setup-swap.sh 128") && !strcontains(local.bootstrap_sh, "setup-swap.sh 256")
    error_message = "the Alpine swap file must be 128 MB on its 1 GB root"
  }
}
