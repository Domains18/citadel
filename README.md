# Citadel

A declarative CDK deployment framework for AWS ECS/Fargate.

## Overview

Citadel makes container deployments as simple as the Serverless Framework experience — but targeting ECS/Fargate instead of Lambda. You declare your app once in a `citadel.yml`. The tool handles SSM secret syncing, CDK infrastructure deployment, Docker build/push, and ECS service rollout in a single command.

```bash
citadel deploy --env dev --deploy-infra
```

## The Problem

Deploying ECS applications involves managing configuration across multiple files:
- Secret lists in deployment scripts
- Duplicate secret definitions in CDK code
- Infrastructure config scattered across multiple files

When you add a new secret (e.g. `INSTAGRAM_APP_ID`), you must update multiple places manually. Miss one and the ECS task fails with cryptic errors, potentially leaving your CloudFormation stack in `UPDATE_ROLLBACK_FAILED` state.

## The Solution

**Single source of truth:** `citadel.yml` defines everything about your deployment.

Both the CLI tool and CDK constructs read from the same file. Write a secret name once, use it everywhere.

## Installation

```bash
# Go
go install github.com/ClusterBox/citadel/cmd/citadel@latest

# From source
git clone https://github.com/ClusterBox/citadel.git
cd citadel
make install
```

## Usage

### Full deployment pipeline

```bash
citadel deploy --env dev --deploy-infra
```

## Roadmap

- [x] Project architecture
- [ ] CLI scaffolding
- [ ] Config parser
- [ ] SSM secret sync
- [ ] Docker build/push
- [ ] ECS deployment
- [ ] CDK construct library

## Configuration

### `queues:` — SQS access (optional)

Grants the ECS task role least-privilege access to existing SQS queues.
Queues are split by intent:

```yaml
queues:
  consume:
    - arn:aws:sqs:us-east-1:123456789012:incoming
  produce:
    - arn:aws:sqs:us-east-1:123456789012:outgoing
```

- `consume` queues are granted `sqs:ReceiveMessage`, `sqs:DeleteMessage`,
  `sqs:GetQueueAttributes`, and `sqs:ChangeMessageVisibility`.
- `produce` queues are granted `sqs:SendMessage` and `sqs:GetQueueAttributes`.

A queue ARN may appear in both lists if the service both reads and writes it.
Citadel does not create the queues — they must already exist.

## License

MIT
