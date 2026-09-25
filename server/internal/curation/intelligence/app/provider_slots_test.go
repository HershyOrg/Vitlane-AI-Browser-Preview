package app

import (
	"sync"
	"testing"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
)

func TestAcquireAvailableNeverExceedsCapacityUnderConcurrentDispatch(t *testing.T) {
	const capacity = 4
	registry := ProviderRegistry{
		providers: map[intelligencedomain.ProviderKind]Provider{},
		slots: map[intelligencedomain.ProviderKind]chan struct{}{
			intelligencedomain.ProviderManaged: make(chan struct{}, capacity),
		},
	}

	// Two dispatch passes overlapping is exactly the case a free-slot reading
	// gets wrong: both would size a claim from the same reading and together
	// take more than the provider allows.
	var held sync.Mutex
	total := 0
	peak := 0
	var group sync.WaitGroup
	for pass := 0; pass < 8; pass++ {
		group.Add(1)
		go func() {
			defer group.Done()
			releases := registry.AcquireAvailable(
				intelligencedomain.ProviderManaged, capacity,
			)
			held.Lock()
			total += len(releases)
			if total > peak {
				peak = total
			}
			held.Unlock()

			held.Lock()
			total -= len(releases)
			held.Unlock()
			for _, release := range releases {
				release()
			}
		}()
	}
	group.Wait()

	if peak > capacity {
		t.Fatalf("held %d slots at once for a capacity of %d", peak, capacity)
	}
	if free := registry.AcquireAvailable(
		intelligencedomain.ProviderManaged, capacity,
	); len(free) != capacity {
		t.Fatalf(
			"every slot must be free again after release, got %d of %d",
			len(free), capacity,
		)
	}
}

func TestAcquireAvailableReturnsWhatIsFreeWithoutBlocking(t *testing.T) {
	registry := ProviderRegistry{
		providers: map[intelligencedomain.ProviderKind]Provider{},
		slots: map[intelligencedomain.ProviderKind]chan struct{}{
			intelligencedomain.ProviderManaged: make(chan struct{}, 2),
		},
	}

	first := registry.AcquireAvailable(intelligencedomain.ProviderManaged, 1)
	if len(first) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(first))
	}
	// Asking for more than remains returns the remainder rather than waiting,
	// so a dispatch tick is never blocked by a running job.
	rest := registry.AcquireAvailable(intelligencedomain.ProviderManaged, 5)
	if len(rest) != 1 {
		t.Fatalf("expected the 1 remaining slot, got %d", len(rest))
	}
	if none := registry.AcquireAvailable(
		intelligencedomain.ProviderManaged, 3,
	); len(none) != 0 {
		t.Fatalf("expected no slots when full, got %d", len(none))
	}

	for _, release := range append(first, rest...) {
		release()
	}
	if free := registry.AcquireAvailable(
		intelligencedomain.ProviderManaged, 2,
	); len(free) != 2 {
		t.Fatalf("expected 2 slots after release, got %d", len(free))
	}
}

func TestAcquireAvailableIgnoresUnknownProviderAndNonPositiveLimit(t *testing.T) {
	registry := ProviderRegistry{
		providers: map[intelligencedomain.ProviderKind]Provider{},
		slots: map[intelligencedomain.ProviderKind]chan struct{}{
			intelligencedomain.ProviderManaged: make(chan struct{}, 1),
		},
	}
	if got := registry.AcquireAvailable("NOT_REGISTERED", 4); got != nil {
		t.Fatalf("unknown provider must yield no slots, got %d", len(got))
	}
	if got := registry.AcquireAvailable(
		intelligencedomain.ProviderManaged, 0,
	); got != nil {
		t.Fatalf("non-positive limit must yield no slots, got %d", len(got))
	}
}
