# DLQ redrive: billing

Queues (Terraform `terraform/billing/erasure.tf`; `local.name` = `{env}-ctech-billing`, `{env}` = `prod`):

| Queue | DLQ | Moves to DLQ after |
|---|---|---|
| `{env}-ctech-billing-erasure` (account-deletion saga participant) | `{env}-ctech-billing-erasure-dlq` | 5 receives |

## Alert

None fires on DLQ depth: the family declined CloudWatch alarms (see the comment in erasure.tf). The symptom is a deletion stuck in `purging` in ctech-account. BYOK sweep problems do raise go-common `alerts` to `ctech-{env}-alerts`, and a failed account-deletion purge also alerts (`erasure-purge-failed`, each redelivery), so a message heading for the DLQ is announced before it lands there. Nothing alerts on DLQ depth itself.

## Inspect

```bash
export AWS_PROFILE=ctech AWS_REGION=us-east-1
Q=$(aws sqs get-queue-url --queue-name <dlq-name> --query QueueUrl --output text)
aws sqs get-queue-attributes --queue-url "$Q" --attribute-names ApproximateNumberOfMessages
aws sqs receive-message --queue-url "$Q" --max-number-of-messages 10 --visibility-timeout 0 \
  --message-attribute-names All
```

`--visibility-timeout 0` peeks without hiding the message. Messages are kept 14 days; read the cause in the
service log group (CloudWatch Logs) before redriving, or the message will just come back.

## Redrive

Fix the cause first, then move the messages back to their source queue:

```bash
aws sqs start-message-move-task --source-arn arn:aws:sqs:us-east-1:868899309401:<dlq-name>
aws sqs list-message-move-tasks --source-arn arn:aws:sqs:us-east-1:868899309401:<dlq-name>
```

Without `--destination-arn` the messages go back to the queue that dead-lettered them. Use `--max-number-of-messages-per-second`
to throttle. Cancel with `aws sqs cancel-message-move-task --task-handle <handle>`.
For a DLQ filled by something that is not a queue (Scheduler, Lambda stream failure) there is no source queue:
pass `--destination-arn` of a queue that can take the payload, or replay by hand and `aws sqs purge-queue` after.
