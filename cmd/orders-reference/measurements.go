package main

import (
	"context"
	"fmt"
	o "github.com/yashok111/mocker/internal/backendobservations"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	ref "github.com/yashok111/mocker/internal/ordersreference"
	"os"
	"strconv"
	"time"
	"uuid"
)

func measureRead(s *ref.Service, variant string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.PrepareReadFixture(ctx); err != nil {
		return err
	}
	execution := uuid.New().String()
	result, err := s.MeasureReadOrder(ctx, variant, execution)
	if err != nil {
		return err
	}
	service, repository := os.Getenv("ORDERS_MEASUREMENT_SERVICE_ID"), os.Getenv("ORDERS_MEASUREMENT_REPOSITORY_ID")
	if !p.ValidID(service) || !p.ValidID(repository) {
		return fmt.Errorf("measurement requires exact ORDERS_MEASUREMENT_SERVICE_ID and ORDERS_MEASUREMENT_REPOSITORY_ID")
	}
	configuration, _ := p.Hash("orders-read-measurement-config-v1", struct{ Fixture, Variant string }{p.FixtureHash(), variant})
	context := o.ObservationContext{Producer: o.Producer{ID: "orders-reference", SchemaVersion: "orders-message-causal-v1", AdapterVersion: "orders-read-v1"}, Source: o.Source{Status: "known", ServiceID: service, RepositoryID: repository, BuildID: result.Identity.BuildHash, SourceFilesHash: result.Identity.SourceTreeHash}, Environment: o.Environment{ID: "isolated-local", Kind: "test"}, Window: o.Window{Start: result.Start, End: result.End}, ConfigurationHash: configuration, Input: o.InputContext{Description: "read order 1; 50 products", Size: new(int64(50)), Unit: new("items")}, Sampling: o.Sampling{Kind: "all", Probability: new("1"), Reason: "all executed operations in this read invocation"}, Instrumentation: o.Instrumentation{SQL: "complete", ExternalCalls: "complete", Retries: "complete", Bytes: "partial", Latency: "complete", Limitations: []string{"Only business read SQL; setup/transaction controls excluded", "Response bytes are encoded JSON, not network transfer", "Notification is an in-process fixture channel, not a broker", "Payment substitute is not invoked by this read scenario"}}}
	if err = o.ValidateContext(context); err != nil {
		return err
	}
	batches := []o.ImportInput{}
	traces := map[string][]o.Record{}
	rootIDs := []string{}
	for _, rec := range result.Records {
		if err = o.ValidateRecord(context, rec); err != nil {
			return err
		}
		trace := rec.TraceID
		if trace == "" {
			trace = "measurements"
		}
		traces[trace] = append(traces[trace], rec)
		if rec.Type == "span" && rec.ParentSpanID == "" && rec.Kind == "server" {
			rootIDs = append(rootIDs, rec.SpanID)
		}
	}
	for trace, records := range traces {
		batches = append(batches, o.ImportInput{Mode: "create", Context: &context, Name: variant + "/" + trace, BatchID: execution + "/" + trace, IdempotencyKey: execution + "/" + trace, Records: records})
	}
	out := struct {
		ExecutionID string              `json:"executionId"`
		RootSpanIDs []string            `json:"rootSpanIds"`
		Measurement ref.ReadMeasurement `json:"measurement"`
		Imports     []o.ImportInput     `json:"imports"`
		ExpectedSQL string              `json:"expectedSQL"`
	}{execution, rootIDs, result, batches, strconv.Itoa(map[string]int{"n_plus_one": 51, "batched": 2}[variant])}
	raw, err := p.Encode(out)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(raw, '\n'))
	return err
}
