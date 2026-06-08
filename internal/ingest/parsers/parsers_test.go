package parsers

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func ev(msg, stream, id string, ts int64) cwtypes.FilteredLogEvent {
	return cwtypes.FilteredLogEvent{
		Message:       aws.String(msg),
		LogStreamName: aws.String(stream),
		EventId:       aws.String(id),
		Timestamp:     aws.Int64(ts),
	}
}

func TestNestJS_Flags500WithMessage(t *testing.T) {
	p := NestJS{}
	got, ok := p.Parse(ev(`{"level":"error","statusCode":500,"message":"db down","requestId":"abc","method":"POST","path":"/orders"}`, "s", "e1", 1000))
	if !ok {
		t.Fatal("expected to flag 500")
	}
	if got.Status != 500 || got.RequestID != "abc" {
		t.Fatalf("wrong fields: %+v", got)
	}
	if got.Message != "POST /orders — db down" {
		t.Fatalf("message build wrong: %q", got.Message)
	}
}

func TestNestJS_IgnoresInfoLines(t *testing.T) {
	p := NestJS{}
	if _, ok := p.Parse(ev(`{"level":"info","statusCode":200,"message":"ok"}`, "s", "e", 1)); ok {
		t.Fatal("expected to skip info line")
	}
}

func TestNestJS_IgnoresPlainText(t *testing.T) {
	p := NestJS{}
	if _, ok := p.Parse(ev(`[Nest] 1 - Bootstrapping application`, "s", "e", 1)); ok {
		t.Fatal("expected to skip non-JSON")
	}
}

func TestNestJS_HandlesNestedMessageObject(t *testing.T) {
	p := NestJS{}
	got, ok := p.Parse(ev(`{"level":"error","message":{"message":"validation failed","error":"BadRequest"}}`, "s", "e", 1))
	if !ok {
		t.Fatal("expected to flag error")
	}
	if got.Message != "validation failed" {
		t.Fatalf("nested message decode wrong: %q", got.Message)
	}
}

func TestGoLambda_FlagsSlogError(t *testing.T) {
	p := GoLambda{}
	got, ok := p.Parse(ev(`{"level":"ERROR","msg":"queue publish failed","requestId":"r-1"}`, "s", "e", 5))
	if !ok {
		t.Fatal("expected to flag slog error")
	}
	if got.Message != "queue publish failed" || got.RequestID != "r-1" {
		t.Fatalf("wrong: %+v", got)
	}
}

func TestGoLambda_IgnoresSlogInfo(t *testing.T) {
	p := GoLambda{}
	if _, ok := p.Parse(ev(`{"level":"INFO","msg":"ok"}`, "s", "e", 1)); ok {
		t.Fatal("expected to skip info")
	}
}

func TestGoLambda_IgnoresStartEnd(t *testing.T) {
	p := GoLambda{}
	if _, ok := p.Parse(ev(`START RequestId: 11111111-2222-3333-4444-555555555555 Version: $LATEST`, "s", "e", 1)); ok {
		t.Fatal("expected to skip START")
	}
	if _, ok := p.Parse(ev(`END RequestId: 11111111-2222-3333-4444-555555555555`, "s", "e", 1)); ok {
		t.Fatal("expected to skip END")
	}
}

func TestGoLambda_FlagsPanic(t *testing.T) {
	p := GoLambda{}
	got, ok := p.Parse(ev(`panic: nil map assignment`, "s", "e", 1))
	if !ok {
		t.Fatal("expected to flag panic")
	}
	if got.Level != "error" {
		t.Fatalf("expected level=error, got %q", got.Level)
	}
}
