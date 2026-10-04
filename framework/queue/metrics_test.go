package queue

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The instruments reach the reader with the labels the dashboards group by.
// A manual reader collects what the process recorded, so this pins the names,
// the units, and the attribute sets without a live collector.
func TestTheQueueInstrumentsReachTheReader(t *testing.T) {
	prev := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	m := newTaskMetrics(ClientConfig{})
	m.recordEnqueued(context.Background(), "cleanup")
	m.recordClaimError(context.Background())
	m.recordOutcome(context.Background(), "cleanup", outcomeSuccess, 250*time.Millisecond, 2)
	m.recordOutcome(context.Background(), "cleanup", outcomeDead, 1200*time.Millisecond, 3)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	type point struct {
		attrs  map[string]string
		value  float64
		count  uint64
		isHist bool
	}
	got := map[string][]point{}
	for _, scope := range rm.ScopeMetrics {
		for _, sm := range scope.Metrics {
			name := sm.Name
			switch data := sm.Data.(type) {
			case metricdata.Sum[float64]:
				for _, dp := range data.DataPoints {
					got[name] = append(got[name], point{attrs: attrMap(dp.Attributes), value: dp.Value})
				}
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					got[name] = append(got[name], point{attrs: attrMap(dp.Attributes), value: float64(dp.Value)})
				}
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					got[name] = append(got[name], point{attrs: attrMap(dp.Attributes),
						value: dp.Sum, count: dp.Count, isHist: true})
				}
			case metricdata.Histogram[int64]:
				for _, dp := range data.DataPoints {
					got[name] = append(got[name], point{attrs: attrMap(dp.Attributes),
						value: float64(dp.Sum), count: dp.Count, isHist: true})
				}
			default:
				t.Fatalf("unexpected instrument type for %s: %T", name, sm.Data)
			}
		}
	}

	find := func(name string, want map[string]string) (point, bool) {
		for _, p := range got[name] {
			match := true
			for k, v := range want {
				if p.attrs[k] != v {
					match = false
					break
				}
			}
			if match {
				return p, true
			}
		}
		return point{}, false
	}

	if _, ok := find("queue.tasks.enqueued", map[string]string{"queue": "cleanup"}); !ok {
		t.Error("queue.tasks.enqueued{queue=cleanup} is missing")
	}
	if _, ok := find("queue.claims.failed", nil); !ok {
		t.Error("queue.claims.failed is missing")
	}
	p, ok := find("queue.task.duration", map[string]string{"queue": "cleanup"})
	if !ok || !p.isHist || p.count != 2 {
		t.Fatalf("queue.task.duration: got %+v (found=%v)", p, ok)
	}
	if p.value < 1.4 || p.value > 1.5 {
		t.Errorf("queue.task.duration sum = %v, want 0.25+1.2", p.value)
	}
	if _, ok := find("queue.task.attempts", map[string]string{"queue": "cleanup"}); !ok {
		t.Error("queue.task.attempts is missing")
	}
}

func attrMap(set attribute.Set) map[string]string {
	out := map[string]string{}
	for _, kv := range set.ToSlice() {
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}
