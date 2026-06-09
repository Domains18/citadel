# Design: `queues:` consume/produce SQS grants

**Date:** 2026-05-16
**Status:** Approved
**Scope:** Single implementation plan.

## Problem

A Citadel-deployed ECS service that uses Amazon SQS needs IAM permission on its
*task role* to call SQS — knowing the queue URL is not enough. Today consumers
hand-write that IAM policy in their per-project CDK, which is exactly the
boilerplate Citadel exists to eliminate. The legolas service, for example,
reads from one queue and writes to another but has no declarative way to grant
that access.

An in-progress (uncommitted) change added a flat `queues: []string` field that
grants all five SQS actions to every listed queue. This design supersedes that
flat shape: it splits permissions by intent so a pure consumer queue is not
granted `SendMessage` and a pure producer queue is not granted
`ReceiveMessage`/`DeleteMessage` — least privilege by construction.

## Non-goals

- The broader `iam:` block (S3, DynamoDB, Secrets Manager, custom statements) —
  remains a documented future step in `AUDIT.md`.
- Creating SQS queues. Citadel grants access to queues that already exist.
- Per-environment queue overrides. `queues:` is global config in this pass.

## Schema

`pkg/config/schema.go`:

```go
type DeployConfig struct {
    // ...existing fields...
    Queues *QueuesConfig `yaml:"queues,omitempty"`
}

// QueuesConfig declares the SQS queues a service may access, split by intent.
type QueuesConfig struct {
    Consume []string `yaml:"consume,omitempty"`
    Produce []string `yaml:"produce,omitempty"`
}
```

`citadel.yml`:

```yaml
queues:
  consume:
    - arn:aws:sqs:us-east-1:454066810976:legolas-incoming-messages
  produce:
    - arn:aws:sqs:us-east-1:454066810976:legolas-outgoing-messages
```

`queues:` is optional. Omitting it, or supplying empty lists, grants nothing.
A queue ARN may appear in both `consume` and `produce`; it then receives both
policy statements.

## IAM behaviour

`buildIAMRoles` in `pkg/constructs/deployable/service.go` attaches up to two
policy statements to the **task role** (not the execution role):

- **consume statement** — emitted when `Consume` is non-empty. Actions:
  `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:GetQueueAttributes`,
  `sqs:ChangeMessageVisibility`. Resources: the `Consume` ARNs.
- **produce statement** — emitted when `Produce` is non-empty. Actions:
  `sqs:SendMessage`, `sqs:GetQueueAttributes`. Resources: the `Produce` ARNs.

`GetQueueAttributes` appears in both because both consumers and producers
legitimately need it. The existing flat-list code (`Queues []string` plus its
single combined policy statement) is replaced, not retained.

## Validation

`DeployConfig.Validate()` checks every entry of `Consume` and `Produce` when
`Queues` is non-nil. Each must be a well-formed SQS ARN:
`arn:aws:sqs:<region>:<account-id>:<queue-name>` — six colon-separated
segments, literal `arn`/`aws`/`sqs` prefixes, non-empty region, account, and
name. A failure returns a clear error naming the field and index, e.g.
`queues.consume[1]: %q is not a valid SQS ARN`. An absent `queues:` block is
valid.

## Testing

New file `pkg/config/schema_test.go` — the first test coverage for
`pkg/config`. Cases:

- valid `consume`/`produce` ARNs pass validation;
- a malformed ARN in `consume` fails with the indexed error;
- a malformed ARN in `produce` fails with the indexed error;
- an omitted `queues:` block passes;
- empty `consume`/`produce` lists pass.

Tests construct `DeployConfig` values directly and assert on `Validate()`;
they do not exercise CDK synthesis.

## Docs & examples

- `examples/legolas/citadel.yml` — add a `queues:` block using the
  `legolas-incoming-messages` (consume) and `legolas-outgoing-messages`
  (produce) queues the service already references via env vars.
- `README.md` and `docs/GETTING_STARTED.md` — document the `queues:` block,
  the consume/produce split, and the exact actions each grants.

## Files touched

- `pkg/config/schema.go` — `QueuesConfig` type, field, validation.
- `pkg/config/schema_test.go` — new.
- `pkg/constructs/deployable/service.go` — two-statement IAM logic in
  `buildIAMRoles`.
- `examples/legolas/citadel.yml` — example `queues:` block.
- `README.md`, `docs/GETTING_STARTED.md` — documentation.

## Follow-up (separate specs, not this pass)

1. Neutral, non-legolas docs and examples; a `citadel init` scaffold.
2. Per-environment config overrides to stop ARNs/accounts leaking across
   environments (audit Tier 1).
3. Lambda support — `kind` discriminator, `functions:` block, `lambdable`
   construct (audit Tier 3).
