# Queues consume/produce SQS grants — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a `citadel.yml` declare SQS queues under a `queues:` block split into `consume`/`produce`, and have Citadel grant the ECS task role least-privilege SQS permissions for them.

**Architecture:** Add a `QueuesConfig` struct to the config schema with `Consume` and `Produce` ARN lists, validated as well-formed SQS ARNs. The `buildIAMRoles` CDK function emits up to two scoped IAM policy statements on the task role. Replaces an in-progress flat `Queues []string` field.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, AWS CDK v2 Go libraries (`awsiam`), Go `testing`.

---

## File Structure

- `pkg/config/schema.go` — `QueuesConfig` type, `Queues` field, ARN validation. Modified.
- `pkg/config/schema_test.go` — unit tests for queue validation. **New file.**
- `pkg/constructs/deployable/service.go` — two-statement SQS IAM logic in `buildIAMRoles`. Modified (replaces existing flat-list block at lines 177-193).
- `examples/legolas/citadel.yml` — example `queues:` block. Modified.
- `README.md`, `docs/GETTING_STARTED.md` — documentation. Modified.

---

## Task 1: Config schema — `QueuesConfig` type and ARN validation

**Files:**
- Modify: `pkg/config/schema.go`
- Test: `pkg/config/schema_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `pkg/config/schema_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

// baseValidConfig returns a DeployConfig that passes Validate() so tests can
// isolate the queues-specific behaviour.
func baseValidConfig() *DeployConfig {
	return &DeployConfig{
		Name:   "demo",
		Region: "us-east-1",
		Container: ContainerConfig{
			Port:   3000,
			CPU:    256,
			Memory: 512,
		},
		Environments: map[string]EnvConfig{
			"dev": {Account: "111111111111"},
		},
		Secrets: []string{"DATABASE_URL"},
	}
}

func TestValidate_NoQueuesBlockIsValid(t *testing.T) {
	cfg := baseValidConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidate_EmptyQueueListsAreValid(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidate_ValidConsumeAndProduceArnsPass(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{
		Consume: []string{"arn:aws:sqs:us-east-1:111111111111:incoming"},
		Produce: []string{"arn:aws:sqs:us-east-1:111111111111:outgoing"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidate_MalformedConsumeArnFails(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{
		Consume: []string{"arn:aws:sqs:us-east-1:111111111111:ok", "not-an-arn"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for malformed consume ARN, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "queues.consume[1]") {
		t.Fatalf("expected error to name queues.consume[1], got %q", got)
	}
}

func TestValidate_MalformedProduceArnFails(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{
		Produce: []string{"arn:aws:s3:::wrong-service"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for malformed produce ARN, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "queues.produce[0]") {
		t.Fatalf("expected error to name queues.produce[0], got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/config/`
Expected: FAIL — compilation error, `undefined: QueuesConfig`.

- [ ] **Step 3: Add the `QueuesConfig` type and `Queues` field**

In `pkg/config/schema.go`, replace the in-progress flat field. The `DeployConfig`
struct currently has `Queues []string \`yaml:"queues,omitempty"\``. Change that
line to:

```go
	Queues       *QueuesConfig          `yaml:"queues,omitempty"`
```

Then add this type immediately after the `DeployConfig` struct definition:

```go
// QueuesConfig declares the SQS queues a service may access, split by intent.
// Consume queues receive read/delete permissions; produce queues receive send
// permissions. A queue ARN may appear in both lists.
type QueuesConfig struct {
	Consume []string `yaml:"consume,omitempty"`
	Produce []string `yaml:"produce,omitempty"`
}
```

- [ ] **Step 4: Add ARN validation to `Validate()`**

In `pkg/config/schema.go`, add this call inside `Validate()` immediately before
the final `return nil`:

```go
	if err := c.validateQueues(); err != nil {
		return err
	}
```

Then add these two methods after `Validate()`:

```go
// validateQueues checks that every queue ARN under queues: is a well-formed
// SQS ARN. An absent queues: block is valid.
func (c *DeployConfig) validateQueues() error {
	if c.Queues == nil {
		return nil
	}
	for i, arn := range c.Queues.Consume {
		if !isValidSQSARN(arn) {
			return fmt.Errorf("queues.consume[%d]: %q is not a valid SQS ARN", i, arn)
		}
	}
	for i, arn := range c.Queues.Produce {
		if !isValidSQSARN(arn) {
			return fmt.Errorf("queues.produce[%d]: %q is not a valid SQS ARN", i, arn)
		}
	}
	return nil
}

// isValidSQSARN reports whether s has the shape
// arn:aws:sqs:<region>:<account-id>:<queue-name> with all six segments present.
func isValidSQSARN(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return false
	}
	if parts[0] != "arn" || parts[1] != "aws" || parts[2] != "sqs" {
		return false
	}
	// region, account-id, queue-name must all be non-empty.
	return parts[3] != "" && parts[4] != "" && parts[5] != ""
}
```

Add `"strings"` to the import block in `pkg/config/schema.go` (it currently
imports `"fmt"`, `"os"`, and `"gopkg.in/yaml.v3"`):

```go
import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/config/`
Expected: PASS — all five `TestValidate_*` tests pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/config/schema.go pkg/config/schema_test.go
git commit -m "feat: add queues consume/produce config with SQS ARN validation

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: IAM — two scoped SQS policy statements on the task role

**Files:**
- Modify: `pkg/constructs/deployable/service.go` (replace lines 177-193, the
  `// Grant SQS access to declared queues` block)

There is no unit test here — `buildIAMRoles` returns CDK constructs and the
spec explicitly excludes CDK synthesis from tests. Verification is `go build`.

- [ ] **Step 1: Replace the flat-list IAM block**

In `pkg/constructs/deployable/service.go`, find the existing block inside
`buildIAMRoles` that starts with the comment `// Grant SQS access to declared
queues` and ends just before `return executionRole, taskRole`. Replace that
entire block (the `if len(cfg.Queues) > 0 { ... }`) with:

```go
	// Grant scoped SQS access to declared queues.
	if cfg.Queues != nil {
		if len(cfg.Queues.Consume) > 0 {
			taskRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
				Actions: jsii.Strings(
					"sqs:ReceiveMessage",
					"sqs:DeleteMessage",
					"sqs:GetQueueAttributes",
					"sqs:ChangeMessageVisibility",
				),
				Resources: arnPointers(cfg.Queues.Consume),
			}))
		}
		if len(cfg.Queues.Produce) > 0 {
			taskRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
				Actions: jsii.Strings(
					"sqs:SendMessage",
					"sqs:GetQueueAttributes",
				),
				Resources: arnPointers(cfg.Queues.Produce),
			}))
		}
	}
```

- [ ] **Step 2: Add the `arnPointers` helper**

Still in `pkg/constructs/deployable/service.go`, add this helper function
immediately after `buildIAMRoles` (after its closing `}`):

```go
// arnPointers converts a slice of ARN strings into the *[]*string form the
// CDK Resources field expects.
func arnPointers(arns []string) *[]*string {
	ptrs := make([]*string, 0, len(arns))
	for _, arn := range arns {
		ptrs = append(ptrs, jsii.String(arn))
	}
	return &ptrs
}
```

- [ ] **Step 3: Verify the build compiles**

Run: `go build ./...`
Expected: exit 0, no output.

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: PASS — `pkg/config` and `internal/env` tests pass; other packages
report `no test files`.

- [ ] **Step 5: Commit**

```bash
git add pkg/constructs/deployable/service.go
git commit -m "feat: grant scoped SQS IAM by consume/produce intent

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Example and documentation

**Files:**
- Modify: `examples/legolas/citadel.yml`
- Modify: `README.md`
- Modify: `docs/GETTING_STARTED.md`

- [ ] **Step 1: Add a `queues:` block to the legolas example**

In `examples/legolas/citadel.yml`, add the following block immediately after
the `secrets:` list and before the `vpc:` block:

```yaml
queues:
  consume:
    - arn:aws:sqs:us-east-1:454066810976:legolas-incoming-messages
  produce:
    - arn:aws:sqs:us-east-1:454066810976:legolas-outgoing-messages
```

- [ ] **Step 2: Verify the example still parses**

Run: `go test ./pkg/config/`
Expected: PASS (unchanged — this confirms nothing regressed).

Then sanity-check the YAML is well-formed:
Run: `go run ./cmd/citadel --help`
Expected: exit 0 (the binary builds and runs).

- [ ] **Step 3: Document `queues:` in the README**

In `README.md`, find the section that describes the `citadel.yml` schema /
configuration options. Add a `queues:` subsection containing:

```markdown
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
```

If `README.md` has no schema/config section, add the subsection at the end of
the document under a new `## Configuration` heading.

- [ ] **Step 4: Document `queues:` in the getting-started guide**

In `docs/GETTING_STARTED.md`, find where the `citadel.yml` fields are walked
through and add a short paragraph after the `secrets:` explanation:

```markdown
### Granting SQS access

If your service uses Amazon SQS, declare the queue ARNs under `queues:` so
Citadel grants the task role permission to use them:

```yaml
queues:
  consume:
    - arn:aws:sqs:us-east-1:123456789012:incoming
  produce:
    - arn:aws:sqs:us-east-1:123456789012:outgoing
```

`consume` queues get read/delete permissions; `produce` queues get send
permissions. The queues must already exist — Citadel does not create them.
```

If `docs/GETTING_STARTED.md` has no per-field walkthrough, add this as a new
`## Granting SQS access` section before the final section of the document.

- [ ] **Step 5: Commit**

```bash
git add examples/legolas/citadel.yml README.md docs/GETTING_STARTED.md
git commit -m "docs: document queues consume/produce block

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Done criteria

- `go build ./...` succeeds.
- `go test ./...` passes, including the five new `pkg/config` tests.
- `examples/legolas/citadel.yml` has a `queues:` block.
- `README.md` and `docs/GETTING_STARTED.md` document the `queues:` block.
- No `Queues []string` flat field remains anywhere in the codebase.
