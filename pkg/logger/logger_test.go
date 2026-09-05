package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestLogger_AddsRequestIDFromSpan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := NewWithWriter(&buf, Config{Level: slog.LevelInfo})

	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	log.InfoContext(ctx, "hello", slog.String("user_id", "u1"))

	var got map[string]any
	if err = json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("json: %v raw=%s", err, buf.String())
	}
	if got["trace_id"] != traceID.String() {
		t.Fatalf("trace_id: %v", got["request_id"])
	}
	if got["span_id"] != spanID.String() {
		t.Fatalf("span_id: %v", got["span_id"])
	}
	if got["user_id"] != "u1" {
		t.Fatalf("user_id: %v", got["user_id"])
	}
}

func TestLogger_NoRequestIDWithoutSpan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := NewWithWriter(&buf, Config{})
	log.InfoContext(context.Background(), "boot")

	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("unexpected request_id: %s", buf.String())
	}
}

func TestLogger_NotSampledNoTelemetryAttrs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := NewWithWriter(&buf, Config{})

	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.TraceFlags(0), //not sampled
	}))

	log.InfoContext(ctx, "hello", slog.String("user_id", "uuid"))

	var got map[string]any

	if err = json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("json: %v raw=%s", err, buf.String())
	}

	if _, ok := got["trace_id"]; ok {
		t.Error("trace_id should not be present when not sampled")
	}
	if _, ok := got["span_id"]; ok {
		t.Error("span_id should not be present when not sampled")
	}
	if _, ok := got["request_id"]; ok {
		t.Error("request_id should not be present when not sampled")
	}

	if got["user_id"] != "uuid" {
		t.Errorf("user_id: got %v, want u1", got["user_id"])
	}
	if got["msg"] != "hello" {
		t.Errorf("msg: got %v, want hello", got["msg"])
	}
}
