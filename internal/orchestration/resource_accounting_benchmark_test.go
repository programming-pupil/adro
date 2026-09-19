package orchestration

import (
	"fmt"
	"testing"
	"time"
)

const schedulerBenchmarkRequests = 1024

func benchmarkAdmissionRequest(id, tenant string, priority int, submitted time.Time) AdmissionRequest {
	return AdmissionRequest{
		ID: id, PlanID: "benchmark-plan", NodeID: "node-" + id,
		Scope:     ResourceScope{TenantID: tenant, WorkspaceID: "benchmark-workspace", AgentID: "benchmark-agent"},
		Resources: ResourceVector{Tokens: 1, ConcurrencySlots: 1}, Priority: priority,
		Persisted: true, SchedulingCost: 1, SubmittedAt: submitted,
	}
}

func BenchmarkFairAdmissionNoisyNeighbor(b *testing.B) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	policy := FairQueuePolicy{
		AgingInterval: time.Second, MaxPriority: 100, MaxPending: schedulerBenchmarkRequests + 1,
		TenantWeights: map[string]int64{"noisy": 1, "quiet": 4},
	}
	b.ReportAllocs()
	b.ReportMetric(schedulerBenchmarkRequests, "requests/op")
	for iteration := 0; iteration < b.N; iteration++ {
		queue, err := NewFairAdmissionQueue(policy)
		if err != nil {
			b.Fatal(err)
		}
		for index := 0; index < schedulerBenchmarkRequests; index++ {
			tenant := "noisy"
			if index%10 == 0 {
				tenant = "quiet"
			}
			if _, err := queue.Submit(benchmarkAdmissionRequest(fmt.Sprintf("%d-%04d", iteration, index), tenant, 20, base)); err != nil {
				b.Fatal(err)
			}
		}
		claimed, err := queue.Claim(base, schedulerBenchmarkRequests)
		if err != nil || len(claimed) != schedulerBenchmarkRequests {
			b.Fatalf("claim count=%d err=%v", len(claimed), err)
		}
		quietInFirstWindow := false
		for _, decision := range claimed[:16] {
			if decision.RequestID[len(decision.RequestID)-4:] == "0000" || decision.VirtualFinish < claimed[15].VirtualFinish {
				quietInFirstWindow = true
				break
			}
		}
		if !quietInFirstWindow {
			b.Fatal("weighted quiet tenant was starved by noisy neighbor")
		}
	}
}

func BenchmarkFairAdmissionBurst(b *testing.B) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	const burstSize = 4096
	policy := FairQueuePolicy{AgingInterval: time.Second, MaxPriority: 100, MaxPending: burstSize, DefaultWeight: 1}
	b.ReportAllocs()
	b.ReportMetric(burstSize, "requests/op")
	for iteration := 0; iteration < b.N; iteration++ {
		queue, err := NewFairAdmissionQueue(policy)
		if err != nil {
			b.Fatal(err)
		}
		for index := 0; index < burstSize; index++ {
			request := benchmarkAdmissionRequest(fmt.Sprintf("burst-%d-%04d", iteration, index), fmt.Sprintf("tenant-%02d", index%32), index%100, base.Add(time.Duration(index%17)*time.Microsecond))
			if _, err := queue.Submit(request); err != nil {
				b.Fatal(err)
			}
		}
		claimed, err := queue.Claim(base.Add(time.Second), burstSize)
		if err != nil || len(claimed) != burstSize || queue.Pending() != 0 {
			b.Fatalf("burst claim count=%d pending=%d err=%v", len(claimed), queue.Pending(), err)
		}
	}
}

func BenchmarkAdmissionQuotaExhaustion(b *testing.B) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	const waitingRequests = 256
	b.ReportAllocs()
	b.ReportMetric(waitingRequests+1, "requests/op")
	for iteration := 0; iteration < b.N; iteration++ {
		ledger, err := NewResourceLedger("", ResourceLedgerOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if err := ledger.SetQuota(ResourceQuota{
			Scope:     ResourceScope{TenantID: "tenant", WorkspaceID: "benchmark-workspace"},
			HardLimit: ResourceVector{Tokens: 10, ConcurrencySlots: 1},
		}); err != nil {
			b.Fatal(err)
		}
		queue, err := NewFairAdmissionQueue(FairQueuePolicy{AgingInterval: time.Second, MaxPending: waitingRequests})
		if err != nil {
			b.Fatal(err)
		}
		controller := AdmissionController{Ledger: ledger, Queue: queue}
		first := benchmarkAdmissionRequest(fmt.Sprintf("first-%d", iteration), "tenant", 50, base)
		first.Resources = ResourceVector{Tokens: 5, ConcurrencySlots: 1}
		decision, err := controller.TryAdmit(first)
		if err != nil || decision.State != AdmissionAdmitted {
			b.Fatalf("initial admission=%+v err=%v", decision, err)
		}
		for index := 0; index < waitingRequests; index++ {
			request := benchmarkAdmissionRequest(fmt.Sprintf("wait-%d-%04d", iteration, index), "tenant", index%100, base)
			request.Resources = ResourceVector{Tokens: 5, ConcurrencySlots: 1}
			decision, err = controller.TryAdmit(request)
			if err != nil || decision.State != AdmissionWaiting {
				b.Fatalf("waiting admission=%+v err=%v", decision, err)
			}
		}
		if queue.Pending() != waitingRequests {
			b.Fatalf("pending=%d want=%d", queue.Pending(), waitingRequests)
		}
	}
}

func BenchmarkFairAdmissionWorkerJitterRecovery(b *testing.B) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	policy := FairQueuePolicy{AgingInterval: time.Second, MaxPriority: 100, MaxPending: schedulerBenchmarkRequests, ShedThreshold: schedulerBenchmarkRequests / 2}
	b.ReportAllocs()
	b.ReportMetric(schedulerBenchmarkRequests, "requests/op")
	for iteration := 0; iteration < b.N; iteration++ {
		queue, err := NewFairAdmissionQueue(policy)
		if err != nil {
			b.Fatal(err)
		}
		protected := 0
		for index := 0; index < schedulerBenchmarkRequests; index++ {
			request := benchmarkAdmissionRequest(fmt.Sprintf("jitter-%d-%04d", iteration, index), fmt.Sprintf("tenant-%02d", index%16), index%40, base.Add(time.Duration((index*37)%503)*time.Millisecond))
			request.Persisted = index%4 == 0
			request.Recovery = index%16 == 0
			if request.Persisted || request.Recovery {
				protected++
			}
			if _, err := queue.Submit(request); err != nil {
				b.Fatal(err)
			}
		}
		shed := queue.ShedOverload(base.Add(2 * time.Second))
		claimed, err := queue.Claim(base.Add(3*time.Second), schedulerBenchmarkRequests)
		if err != nil {
			b.Fatal(err)
		}
		if len(shed)+len(claimed) != schedulerBenchmarkRequests || len(claimed) < protected {
			b.Fatalf("shed=%d claimed=%d protected=%d", len(shed), len(claimed), protected)
		}
	}
}

func BenchmarkFairAdmissionPriorityInversion(b *testing.B) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	policy := FairQueuePolicy{AgingInterval: time.Millisecond, MaxPriority: 100, MaxPending: schedulerBenchmarkRequests}
	b.ReportAllocs()
	b.ReportMetric(schedulerBenchmarkRequests, "requests/op")
	for iteration := 0; iteration < b.N; iteration++ {
		queue, err := NewFairAdmissionQueue(policy)
		if err != nil {
			b.Fatal(err)
		}
		oldLowID := fmt.Sprintf("old-low-%d", iteration)
		if _, err := queue.Submit(benchmarkAdmissionRequest(oldLowID, "tenant-low", 1, base)); err != nil {
			b.Fatal(err)
		}
		for index := 1; index < schedulerBenchmarkRequests; index++ {
			if _, err := queue.Submit(benchmarkAdmissionRequest(fmt.Sprintf("new-high-%d-%04d", iteration, index), "tenant-high", 99, base.Add(50*time.Millisecond))); err != nil {
				b.Fatal(err)
			}
		}
		claimed, err := queue.Claim(base.Add(200*time.Millisecond), schedulerBenchmarkRequests)
		if err != nil || len(claimed) != schedulerBenchmarkRequests {
			b.Fatalf("claim count=%d err=%v", len(claimed), err)
		}
		found := false
		for _, decision := range claimed[:2] {
			if decision.RequestID == oldLowID && decision.EffectivePriority == policy.MaxPriority {
				found = true
			}
		}
		if !found {
			b.Fatal("aged low-priority request remained inverted behind newer work")
		}
	}
}
