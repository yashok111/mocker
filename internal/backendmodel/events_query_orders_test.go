package backendmodel

import (
	"encoding/json/v2"
	"os"
	"reflect"
	"slices"
	"testing"
)

func eventsQueryItemWitness(i EventsItem) EventsWitness {
	switch i.Kind {
	case "route":
		return i.Route.Witness
	case "job":
		return i.Job.Witness
	case "service_call":
		return i.ServiceCall.Witness
	default:
		return i.Boundary.Witness
	}
}
func eventsQueryItemDispatch(i EventsItem) []EventsDispatch {
	switch i.Kind {
	case "route":
		return i.Route.Dispatch
	case "job":
		return i.Job.Dispatch
	case "service_call":
		return i.ServiceCall.Dispatch
	default:
		return i.Boundary.Dispatch
	}
}

// Expected tuples and handler names come only from the independently reviewed
// source oracle. The adapter supplies UUID identity, never expected route output.
func TestEventsQueryOrdersIndependentOracleAndPureStore(t *testing.T) {
	raw, err := os.ReadFile("testdata/events/orders/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Witnesses map[string]struct {
			Path               string
			StartLine, EndLine int64
		}
		Edges []struct {
			Key       string
			Witnesses []string
		}
		ExpectedViews struct {
			Routes []struct {
				Key, EmitsEdgeKey, DeliveryEdgeKey string
				ExpectedHandlerKeys                []string
			}
			OrphanSubscriptions []struct{ DeliveryEdgeKey, ConsumerKey string }
			Jobs                []string
			ServiceCalls        []struct{ CallEdgeKey, OperationKey, HandlerKey, FlowKey, TargetServiceKey string }
			RelatedRoutes       []string
		}
	}
	if err = json.Unmarshal(raw, &oracle, json.MatchCaseInsensitiveNames(true)); err != nil {
		t.Fatal(err)
	}
	if len(oracle.ExpectedViews.Routes) != 6 || len(oracle.ExpectedViews.OrphanSubscriptions) != 1 || len(oracle.Edges) == 0 || len(oracle.Witnesses) == 0 {
		t.Fatal("independent oracle did not decode its required expectations")
	}
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "baseline", true: "resolved"}[resolved], func(t *testing.T) {
			repo, out, _, ids := eventsOrdersCommitted(t, resolved)
			before := runtimeQueryDBSnapshot(t, repo.db.R)
			page, err := repo.QueryEvents(t.Context(), out.Project.ID, EventsQueryInput{RevisionID: out.Revision.ID, View: "routes"})
			if err != nil {
				t.Fatal(err)
			}
			if page.ProjectID != out.Project.ID || page.RevisionID != out.Revision.ID || page.SemanticHash != out.Revision.SemanticHash || !page.Complete || page.Truncated {
				t.Fatal("source pin or enumeration changed", page)
			}
			if len(page.Items) != len(oracle.ExpectedViews.Routes)+len(oracle.ExpectedViews.OrphanSubscriptions) {
				t.Fatal("additional or omitted routes", page.Items)
			}
			got := map[string]EventsItem{}
			for _, i := range page.Items {
				r := eventsQueryRefs(i)
				got[r.EmitsEdgeID+"/"+r.DeliveryEdgeID] = i
			}
			state, err := loadRevisionState(t.Context(), repo.db.R, out.Project.ID, out.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			evidence := map[string]Evidence{}
			edges := map[string]Edge{}
			for _, e := range state.Evidence {
				evidence[e.ID] = e
			}
			for _, e := range state.Edges {
				edges[e.ExternalKey] = e
			}
			for _, want := range oracle.ExpectedViews.Routes {
				i, ok := got[ids[want.EmitsEdgeKey]+"/"+ids[want.DeliveryEdgeKey]]
				if !ok {
					t.Fatal("missing exact oracle tuple", want)
				}
				refs := eventsQueryRefs(i)
				emit, delivery := edges[want.EmitsEdgeKey], edges[want.DeliveryEdgeKey]
				if refs.ProducerID != emit.From || refs.MessageID != emit.To || refs.ChannelID != delivery.From || refs.ConsumerID != delivery.To {
					t.Fatal("route identity drift", refs)
				}
				handlerKeys := slices.Clone(want.ExpectedHandlerKeys)
				if resolved && want.Key == "route.fraud" {
					handlerKeys = []string{"handler.fraud"}
				}
				actual := []string{}
				dispatch := eventsQueryItemDispatch(i)
				for _, d := range dispatch {
					if d.HandlerID != "" {
						actual = append(actual, d.HandlerID)
					}
				}
				expected := []string{}
				for _, key := range handlerKeys {
					expected = append(expected, ids[key])
				}
				slices.Sort(actual)
				slices.Sort(expected)
				if !slices.Equal(actual, expected) {
					t.Fatalf("%s handlers=%v want%v", want.Key, actual, expected)
				}
				proof := eventsQueryItemWitness(i)
				if proof.Provenance != "source" || !slices.Contains(proof.EdgeIDs, emit.ID) || !slices.Contains(proof.EdgeIDs, delivery.ID) {
					t.Fatal("source route witnesses lost", proof)
				}
				for _, key := range []string{want.EmitsEdgeKey, want.DeliveryEdgeKey} {
					edge := edges[key]
					for _, evidenceID := range edge.EvidenceIDs {
						if !slices.Contains(proof.EvidenceIDs, evidenceID) {
							t.Fatal("exact route evidence omitted", key, evidenceID)
						}
					}
					// Compare available physical source proof to the separately authored oracle.
					for _, expectedEdge := range oracle.Edges {
						if expectedEdge.Key != key {
							continue
						}
						for _, name := range expectedEdge.Witnesses {
							w := oracle.Witnesses[name]
							if !slices.ContainsFunc(edge.EvidenceIDs, func(id string) bool {
								e := evidence[id]
								return e.Source.File == w.Path && e.Source.StartLine != nil && *e.Source.StartLine == w.StartLine && e.Source.EndLine != nil && *e.Source.EndLine == w.EndLine
							}) {
								t.Fatalf("%s lacks independent physical witness %s", key, name)
							}
						}
					}
				}
				if want.Key == "route.primary" || want.Key == "route.secondary" {
					if i.Route != nil && (i.Route.Condition.Status != "known" || i.Route.Group.Status != "known" || string(i.Route.Group.Value) != `"billing"`) {
						t.Fatal("known configured condition/group changed", i.Route)
					}
					if i.Route == nil || i.Route.EmitContext == nil || i.Route.EmitContext.Transaction.TransactionID != ids["tx.cancel"] || i.Route.EmitContext.ControlWitness == nil || !slices.Contains(i.Route.EmitContext.ControlWitness.NodeIDs, refs.ProducerID) {
						t.Fatalf("precommit local context/control proof unavailable: route=%+v context=%+v txids=%v", i.Route, i.Route.EmitContext, ids["tx.cancel"])
					}
					related := []string{}
					for _, r := range i.Route.Related {
						related = append(related, r.EdgeID)
					}
					expectedRelated := make([]string, 0, len(oracle.ExpectedViews.RelatedRoutes))
					for _, key := range oracle.ExpectedViews.RelatedRoutes {
						expectedRelated = append(expectedRelated, ids[key])
					}
					slices.Sort(related)
					slices.Sort(expectedRelated)
					if !slices.Equal(related, expectedRelated) {
						t.Fatal("configured retry/DLQ drift", related)
					}
				}
				if want.Key == "route.audit" {
					if i.Boundary == nil || len(dispatch) != 2 || len(dispatch[0].FlowIDs)+len(dispatch[1].FlowIDs) == 0 {
						t.Fatal("partial dispatch known flow/remainder lost", i)
					}
				}
				if want.Key == "route.legacy" || want.Key == "route.fraud" && !resolved {
					if i.Boundary == nil {
						t.Fatal("unknown handler silently promoted", i)
					}
				}
			}
			for _, orphan := range oracle.ExpectedViews.OrphanSubscriptions {
				i, ok := got["/"+ids[orphan.DeliveryEdgeKey]]
				if !ok || i.Boundary == nil || i.Boundary.References.ConsumerID != ids[orphan.ConsumerKey] {
					t.Fatal("reverse orphan missing", orphan)
				}
			}
			jobs, err := repo.QueryEvents(t.Context(), out.Project.ID, EventsQueryInput{RevisionID: out.Revision.ID, View: "jobs", ServiceID: ids["svc.orders"]})
			if err != nil {
				t.Fatal(err)
			}
			actualJobs := []string{}
			for _, i := range jobs.Items {
				if i.Job == nil {
					t.Fatal("static job boundary unexpected", i)
				}
				if i.Job.References.JobID == ids["job.cron"] && (i.Job.Trigger.Expression.Status != "known" || string(i.Job.Trigger.Expression.Value) != `"0 3 * * *"` || i.Job.Trigger.Timezone.Status != "known" || string(i.Job.Trigger.Timezone.Value) != `"UTC"`) {
					t.Fatal("known static cron scalars changed", i.Job.Trigger)
				}
				actualJobs = append(actualJobs, i.Job.References.JobID)
			}
			expectedJobs := make([]string, 0, len(oracle.ExpectedViews.Jobs))
			for _, key := range oracle.ExpectedViews.Jobs {
				expectedJobs = append(expectedJobs, ids[key])
			}
			slices.Sort(actualJobs)
			slices.Sort(expectedJobs)
			if !slices.Equal(actualJobs, expectedJobs) {
				t.Fatal("static jobs drift", jobs)
			}
			calls, err := repo.QueryEvents(t.Context(), out.Project.ID, EventsQueryInput{RevisionID: out.Revision.ID, View: "service_calls", ServiceID: ids["svc.orders"]})
			if err != nil {
				t.Fatal(err)
			}
			if len(calls.Items) != len(oracle.ExpectedViews.ServiceCalls) {
				t.Fatal("service calls drift", calls)
			}
			for _, want := range oracle.ExpectedViews.ServiceCalls {
				if !slices.ContainsFunc(calls.Items, func(i EventsItem) bool {
					if i.ServiceCall == nil {
						return false
					}
					c := i.ServiceCall
					return c.References.CallsEdgeID == ids[want.CallEdgeKey] && c.References.OperationID == ids[want.OperationKey] && c.References.TargetServiceID == ids[want.TargetServiceKey] && slices.ContainsFunc(c.Dispatch, func(d EventsDispatch) bool {
						return d.HandlerID == ids[want.HandlerKey] && slices.Contains(d.FlowIDs, ids[want.FlowKey])
					})
				}) {
					t.Fatal("explicit call operation/handler/flow drift", want)
				}
			}
			if after := runtimeQueryDBSnapshot(t, repo.db.R); !reflect.DeepEqual(before, after) {
				t.Fatal("query mutated persisted source, pins or receipts")
			}
		})
	}
}
