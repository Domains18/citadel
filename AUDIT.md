# Citadel Audit & Roadmap

## Current state

Citadel is a small, focused CLI (~1.7k LOC) that wraps the legolas-style ECS/Fargate deploy pattern: parse `citadel.yml` → sync `.env` to SSM → docker build/push to ECR → CDK deploy → optional log stream. It's clean, single-purpose, and the "single source of truth" framing is its differentiator.

The schema is leaky, the CDK construct does too much by default, and several core features expected of a Serverless-style framework are missing.

---

## Audit findings (current ECS path)

### Correctness gaps

1. **No per-environment overrides for most fields.** `EnvConfig` only carries account/capacity/spot. CPU, memory, queues, secrets, port — all global. Already biting consumers with hard-coded dev ARNs leaking into prod.
2. **Auto-injected env vars collide with user-declared secrets.** `pkg/constructs/deployable/service.go:238-240` sets `PORT`/`SERVICE_NAME`/`ENVIRONMENT`. If a user lists any of these under `secrets:`, ECS rejects the task definition. Either reserve those names with a config validator, or let users opt out.
3. **`Validate()` requires `len(Secrets) > 0`.** `pkg/config/schema.go:89` — wrong for apps with zero secrets and noisy for examples.
4. **`buildECRRepository` uses `FromRepositoryName`** (`pkg/constructs/deployable/service.go:136`) — assumes the repo already exists out-of-band. First-time deploys will fail with no clear error.
5. **Image tag is always `latest`** (`pkg/constructs/deployable/service.go:244`) — no immutable rollouts. The pipeline already pushes a git-SHA tag; use it.
6. **Hard-coded `x86_64` architecture** (`pkg/constructs/deployable/service.go:215`) — no ARM/Graviton (~20% cheaper).
7. **Hard-coded log retention `ONE_WEEK`** + `RemovalPolicy_DESTROY` (`pkg/constructs/deployable/service.go:202-203`) — unsuitable for prod compliance needs.
8. **No HTTPS on the ALB.** The CloudFront workaround means TLS is only at edge; ALB→Origin is HTTP. No ACM cert support, no custom domain.
9. **CDK is shelled out via `exec.Command("cdk", "deploy", …)`** (`pkg/pipeline/deploy.go:156`) — fragile (assumes `cdk` on PATH, no version pin), and odd given Citadel itself imports CDK Go libs. The construct lives in this repo but each consumer still maintains its own `cdk/main.go`. That's the duplication the README claims to eliminate.
10. **`getAWSAccountID` shells out to `aws sts`** (`pkg/pipeline/deploy.go:263`) but the codebase already has an STS-capable AWS client. Inconsistent and adds an `aws` CLI dependency.
11. **No diff/preview before deploy.** `cdk diff` exists; surfacing it would reduce blind-deploy fear.
12. **No rollback command.** Recovery from a bad deploy means manual ECS console work.
13. **`SyncSecrets` has no delete/prune.** Stale SSM parameters from removed secrets accumulate forever.

### DX gaps

- No `citadel init` to scaffold a new project.
- No `citadel destroy`.
- No `citadel doctor` — checks AWS auth, Docker daemon, IAM perms, ECR repo existence before failing 4 minutes into a deploy.
- No structured output (JSON/quiet mode) — blocks CI integration.
- Logs are `fmt.Printf` with emoji; no log level, no `--verbose`/`--quiet`.
- No tests for `pkg/pipeline` or `pkg/constructs` (only `internal/env/loader_test.go`).
- `version` is hard-coded `"dev"` (`cmd/citadel/main.go:14`) — should be `-ldflags`-injected at build time.

---

## Proposed feature roadmap

### Tier 1 — Fix the schema (biggest leverage, smallest effort)

| Feature | What | Why |
|---|---|---|
| Per-env overrides | Move `cpu`, `memory`, `queues`, `secrets`, etc. into `environments.<env>` (with global fallback) | Unblocks dev/prod separation; fixes hard-coded ARNs |
| Reserved env-var validation | Reject `PORT`/`SERVICE_NAME`/`ENVIRONMENT` in `secrets:` with a clear error | Prevents the ECS rejection bug |
| Env-aware ARN templating | Allow `${env}` in queue/topic/bucket ARNs: `arn:aws:sqs:...:smaug-logistics-${env}` | Cleaner than per-env duplication for common patterns |
| `iam:` block | Generic AWS perm grants beyond just queues — `iam: { sqs: [...], s3: [...], dynamodb: [...], secretsmanager: [...], custom: [...] }` | The `queues:` block is already a precedent; generalize it |

### Tier 2 — Production readiness

| Feature | What |
|---|---|
| Custom domain + ACM cert | `domain: { name: api.example.com, certificate_arn: ... }` → ALB HTTPS listener + Route53 record |
| Image tagging strategy | Use the git-SHA tag the pipeline already produces, not `latest`; enable rollback by tag |
| ARM/Graviton support | `container.architecture: arm64\|amd64` |
| Configurable log retention | `logs: { retention_days: 30, removal_policy: retain }` |
| Auto-create ECR | Don't `FromRepositoryName` blindly; create on first deploy, or check + emit a friendly error |
| `citadel rollback --to <sha>` | Update task def to a previous image tag |
| `citadel diff` | Wraps `cdk diff` + ECS task-def comparison |
| `citadel destroy` | Tear down stack |
| SSM prune on sync | Remove parameters not in `secrets:` (with `--no-prune` opt-out) |

### Tier 3 — Lambda support

The cleanest design is a **kind discriminator** on the config, since ECS-Fargate and Lambda have different shapes:

```yaml
# citadel.yml
name: legolas-worker
kind: lambda           # default: fargate
region: us-east-1

functions:
  process-message:
    runtime: provided.al2023      # or python3.12, nodejs20.x, etc.
    handler: bootstrap
    memory: 512
    timeout: 30
    architecture: arm64
    triggers:
      - sqs:
          arn: arn:aws:sqs:us-east-1:...:legolas-incoming-messages
          batch_size: 10
      - schedule: rate(5 minutes)
      - http:
          path: /webhook
          method: POST
    environment:
      LOG_LEVEL: info

environments:
  dev: { account: "454066810976" }
  prod: { account: "454066810976" }

secrets: [ENCRYPTION_KEY, ...]
```

**Implementation plan:**

1. **Schema**: add `Kind string` (default `fargate`) + `Functions map[string]LambdaFunctionConfig`.
2. **Construct**: new `pkg/constructs/lambdable/function.go` mirroring the deployable pattern. Uses `awslambda.NewFunction` (or `awslambdago.NewGoFunction` for Go).
3. **Triggers**: SQS event source mapping, EventBridge rules, API Gateway HTTP API integration. Each is ~30 lines of CDK.
4. **Build**: For Go → cross-compile to `bootstrap` and zip. For Node/Python → bundle via `aws-lambda-nodejs`/`aws-lambda-python-alpha`. Or accept `image_uri:` for container Lambdas (reuses existing ECR push path).
5. **CLI**: `citadel deploy` dispatches on `kind`. `citadel logs` → CloudWatch logs for the function. `citadel invoke <function> --payload …`.
6. **Secrets**: same SSM pattern — Lambda reads via Parameters and Secrets Lambda Extension or env-var injection at deploy time.

Suggested first slice: **Go runtime + SQS trigger + zip packaging.** Covers Legolas's own background-worker case (the SQS consumers could plausibly be Lambdas instead of ECS workers) and validates the abstraction with one consumer before generalizing.

### Tier 4 — Polish & ergonomics

| Feature | What |
|---|---|
| `citadel init` | Interactive scaffold of `citadel.yml` + `cdk/main.go` + `Dockerfile` |
| `citadel doctor` | Pre-flight: AWS auth, Docker, ECR repo, IAM perms |
| Structured output | `--output json` / `--quiet` for CI |
| `citadel exec` | One-shot ECS-Exec into a running task (no AWS CLI needed) |
| Multi-region deploys | `regions: [us-east-1, eu-west-1]` |
| Drop the `cdk/main.go` boilerplate | Citadel could exec CDK programmatically via the Go libs it already imports, instead of shelling out to a per-project `cdk/` directory |
| Plugin / hook system | `hooks: { pre_deploy: ./scripts/migrate.sh }` |
| GitHub Actions / OIDC helper | `citadel ci` emits a workflow + IAM policy doc |

---

## Recommended sequencing

1. **Week 1**: Tier 1 (schema fixes) — fixes real bugs.
2. **Weeks 1–2**: Image-tag-by-SHA + rollback + diff (Tier 2 essentials) — bigger safety net.
3. **Weeks 2–4**: Lambda support, Go-runtime + SQS trigger only (Tier 3 first slice).
4. **Ongoing**: doctor, init, structured output, tests.
