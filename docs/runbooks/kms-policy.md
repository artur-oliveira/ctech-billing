# KMS policy: SecureStrings, DynamoDB, S3

The KMS bill must stay ~0 (free tier: 20k requests/month). Every SSM
SecureString read with decryption, and every DynamoDB request on a table using
the AWS-managed key (`aws/dynamodb`), is a billed KMS request.

1. **Decrypt at boot, cache in memory.** Never decrypt in a loop, cron job or
   per request. Lambdas read once per cold start (`byok-outbound`, `byok-webhook`
   do). If a value must follow rotation, read it with decryption disabled (ciphertext +
   Version, no KMS call) and decrypt only when it changed, or use a TTL of 1h or more
   and accept that rotation takes up to that long to apply.
2. **DynamoDB uses the AWS-owned key**: `server_side_encryption { enabled = false }`
   in `terraform/billing/dynamodb.tf`. The block must stay explicit: omitting it leaves an
   existing table on KMS (the provider treats the argument as computed). Switching is
   an in-place `UpdateTable`, verified with `terraform plan` (0 destroy, no replacement).
3. **S3 is SSE-S3 (AES256)**, never `aws:kms` (`documents.tf`). The `kms:Decrypt`
   grants in IAM stay only for reading SecureStrings.

## Where billing hit it

The `* * * * * job.sh deliver` cron (bootstrap-alpine) sources
`/opt/app/load-ssm-env.sh`, which decrypted every SecureString each minute
(~1,440 x 3 Decrypts/day). `setup-ssm-env.sh` (ctech-cdk) now caches the
decrypted value on tmpfs (`/run/ctech-ssm`) keyed by the hash of the ciphertext, so a job
costs zero KMS calls until a secret is rotated. The service reads at start only.

Residual KMS requests: three SecureStrings per instance boot plus per rotation.
