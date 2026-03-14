package gateway

import (
	"fmt"
	"sync"
	"testing"
)

func TestShardMap_BasicOps(t *testing.T) {
	sm := NewShardMap[string]()

	// Set and Get
	sm.Set("foo", "bar")
	v, ok := sm.Get("foo")
	if !ok || v != "bar" {
		t.Fatalf("expected (bar, true), got (%s, %v)", v, ok)
	}

	// Missing key
	_, ok = sm.Get("missing")
	if ok {
		t.Fatal("expected missing key to return false")
	}

	// Delete
	sm.Delete("foo")
	_, ok = sm.Get("foo")
	if ok {
		t.Fatal("expected deleted key to return false")
	}

	// Len
	for i := 0; i < 100; i++ {
		sm.Set(fmt.Sprintf("key-%d", i), "val")
	}
	if sm.Len() != 100 {
		t.Fatalf("expected Len=100, got %d", sm.Len())
	}
}

func TestShardMap_Range(t *testing.T) {
	sm := NewShardMap[int]()
	for i := 0; i < 50; i++ {
		sm.Set(fmt.Sprintf("k%d", i), i)
	}

	count := 0
	sm.Range(func(key string, val int) bool {
		count++
		return true
	})
	if count != 50 {
		t.Fatalf("expected Range to visit 50 items, visited %d", count)
	}
}

func TestShardMap_Concurrent(t *testing.T) {
	sm := NewShardMap[int]()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				key := fmt.Sprintf("g%d-k%d", n, j)
				sm.Set(key, n*1000+j)
			}
		}(i)
	}
	wg.Wait()

	if sm.Len() != 100_000 {
		t.Fatalf("expected Len=100000, got %d", sm.Len())
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				key := fmt.Sprintf("g%d-k%d", n, j)
				v, ok := sm.Get(key)
				if !ok || v != n*1000+j {
					t.Errorf("unexpected value for %s: got (%d, %v)", key, v, ok)
				}
			}
		}(i)
	}
	wg.Wait()
}

// generateKeys pre-builds key strings so benchmarks measure map ops, not fmt.Sprintf.
func generateKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("+27831%06d", i) // realistic MSISDN-like keys
	}
	return keys
}

// --- Small map benchmarks (10k entries) ---

func BenchmarkShardMap_Set(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Set(keys[i%10_000], "val")
			i++
		}
	})
}

func BenchmarkShardMap_Get(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000)
	for _, k := range keys {
		sm.Set(k, "val")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Get(keys[i%10_000])
			i++
		}
	})
}

func BenchmarkShardMap_MixedReadWrite(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000)
	for _, k := range keys {
		sm.Set(k, "val")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				sm.Set(keys[i%10_000], "updated")
			} else {
				sm.Get(keys[i%10_000])
			}
			i++
		}
	})
}

// --- Large map benchmarks (1M entries — production scale) ---

func BenchmarkShardMap_Get_1M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(1_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Get(keys[i%1_000_000])
			i++
		}
	})
}

func BenchmarkShardMap_Set_1M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(1_000_000)
	// Pre-fill to 1M so we measure updates at scale, not inserts into empty map.
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Set(keys[i%1_000_000], "conn-99")
			i++
		}
	})
}

func BenchmarkShardMap_MixedReadWrite_1M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(1_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				sm.Set(keys[i%1_000_000], "conn-99")
			} else {
				sm.Get(keys[i%1_000_000])
			}
			i++
		}
	})
}

// --- 10M entries — stress test at extreme scale ---

func BenchmarkShardMap_Get_10M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Get(keys[i%10_000_000])
			i++
		}
	})
}

func BenchmarkShardMap_Set_10M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Set(keys[i%10_000_000], "conn-99")
			i++
		}
	})
}

func BenchmarkShardMap_MixedReadWrite_10M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				sm.Set(keys[i%10_000_000], "conn-99")
			} else {
				sm.Get(keys[i%10_000_000])
			}
			i++
		}
	})
}

func BenchmarkSyncMap_Get_10M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Load(keys[i%10_000_000])
			i++
		}
	})
}

func BenchmarkSyncMap_Set_10M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Store(keys[i%10_000_000], "conn-99")
			i++
		}
	})
}

func BenchmarkSyncMap_MixedReadWrite_10M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				sm.Store(keys[i%10_000_000], "conn-99")
			} else {
				sm.Load(keys[i%10_000_000])
			}
			i++
		}
	})
}

// --- Delete benchmarks at 10M ---

func BenchmarkShardMap_Delete_10M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%10_000_000]
			sm.Delete(k)
			// Re-insert so subsequent iterations still have work to do.
			sm.Set(k, "conn-42")
			i++
		}
	})
}

func BenchmarkSyncMap_Delete_10M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%10_000_000]
			sm.Delete(k)
			sm.Store(k, "conn-42")
			i++
		}
	})
}

// --- Mixed Get/Set/Delete (realistic DLR lifecycle: set on submit, get+delete on DLR) ---

func BenchmarkShardMap_Lifecycle_10M(b *testing.B) {
	sm := NewShardMap[string]()
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Set(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%10_000_000]
			switch i % 10 {
			case 0: // 10% insert (new submit)
				sm.Set(k, "conn-99")
			case 1: // 10% delete (DLR cleanup)
				sm.Delete(k)
			default: // 80% read (DLR lookup)
				sm.Get(k)
			}
			i++
		}
	})
}

func BenchmarkSyncMap_Lifecycle_10M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(10_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%10_000_000]
			switch i % 10 {
			case 0:
				sm.Store(k, "conn-99")
			case 1:
				sm.Delete(k)
			default:
				sm.Load(k)
			}
			i++
		}
	})
}

// --- sync.Map comparison at 1M (baseline) ---

func BenchmarkSyncMap_Get_1M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(1_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Load(keys[i%1_000_000])
			i++
		}
	})
}

func BenchmarkSyncMap_Set_1M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(1_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sm.Store(keys[i%1_000_000], "conn-99")
			i++
		}
	})
}

func BenchmarkSyncMap_MixedReadWrite_1M(b *testing.B) {
	var sm sync.Map
	keys := generateKeys(1_000_000)
	for _, k := range keys {
		sm.Store(k, "conn-42")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				sm.Store(keys[i%1_000_000], "conn-99")
			} else {
				sm.Load(keys[i%1_000_000])
			}
			i++
		}
	})
}
