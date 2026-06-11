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

When you add a new secret, you must update multiple places manually. Miss one and the ECS task fails with cryptic errors, potentially leaving your CloudFormation stack in `UPDATE_ROLLBACK_FAILED` state.

## The Solution

**Single source of truth:** `citadel.yml` defines everything about your deployment.

Both the CLI tool and CDK constructs read from the same file. Write a secret name once, use it everywhere.

## Installation

```bash

# From source
git clone https://github.com/Domains18citadel.git
cd citadel
make install
```

## Usage

### Full deployment pipeline

```bash
citadel deploy --env dev --deploy-infra
```

## citadel-logs daemon

Citadel ships a separate always-on binary, `citadel-logs`, that watches
CloudWatch log groups for every registered service and surfaces
500-class errors at <http://localhost:5500/logs>.

It works across runtimes:

- `runtime: ecs` (default) —  NestJS backends.
- `runtime: lambda` —  Go Lambdas. Requires a
  `lambda: { functionName: ... }` block.

### Run it

```bash
# 1. Build the Docker image
make docker-logs

# 2. Register a repo
cd ~/Documents/github/backend/
citadel logs-daemon register --env dev

# 3. Start the daemon
docker compose -f docker-compose.logs.yml up -d

# 4. Open the dashboard
open http://localhost:5500/logs
```

## License

MIT
