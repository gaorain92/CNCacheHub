package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestTake_AllowsBurst(t *testing.T) {
	l := NewLimiter(5, 1, time.Minute)
	for i := 0; i < 5; i++ {
		ok, _ := l.Take("ip1")
		if !ok {
			t.Errorf("burst %d should be allowed", i)
		}
	}
	// 第 6 个应该被拒
	ok, wait := l.Take("ip1")
	if ok {
		t.Error("6th should be denied")
	}
	if wait <= 0 {
		t.Errorf("retryAfter should be > 0, got %v", wait)
	}
}

func TestTake_Refill(t *testing.T) {
	l := NewLimiter(2, 10, time.Minute) // 10/秒
	// 第一次扣 2 个
	l.Take("a")
	l.Take("a")
	ok, _ := l.Take("a")
	if ok {
		t.Error("3rd should be denied")
	}
	// 等 200ms 应该 refil 2 个
	time.Sleep(200 * time.Millisecond)
	if ok1, _ := l.Take("a"); !ok1 {
		t.Error("after 200ms should refill 1 token")
	}
	if ok2, _ := l.Take("a"); !ok2 {
		t.Error("after 200ms should refill 2 tokens")
	}
}

func TestTake_KeyIsolation(t *testing.T) {
	l := NewLimiter(2, 0.1, time.Minute)
	l.Take("a")
	l.Take("a")
	ok, _ := l.Take("a")
	if ok {
		t.Error("a should be denied")
	}
	// b 独立，应该还能 burst 2 个
	if ok, _ := l.Take("b"); !ok {
		t.Error("b should be allowed (independent bucket)")
	}
}

func TestTake_CapacityCap(t *testing.T) {
	l := NewLimiter(3, 100, time.Minute) // 100/秒 refil
	// 等 1 秒让桶满
	time.Sleep(50 * time.Millisecond)
	// 扣 5 次 — 第 4 5 个应该被拒（桶上限 3）
	for i := 0; i < 3; i++ {
		ok, _ := l.Take("x")
		if !ok {
			t.Errorf("burst %d should be allowed", i)
		}
	}
	ok, _ := l.Take("x")
	if ok {
		t.Error("4th should be denied (cap=3)")
	}
}

func TestStats(t *testing.T) {
	l := NewLimiter(10, 1, time.Minute)
	l.Take("a")
	l.Take("b")
	l.Take("c")
	if l.Stats() != 3 {
		t.Errorf("Stats = %d, want 3", l.Stats())
	}
}

func TestGC(t *testing.T) {
	// gcEvery=20ms, idle=50ms → 等 60ms 后 a 桶 idle 超时，下次 Take 触发 GC
	l := NewLimiterWithGC(10, 1, 50*time.Millisecond, 20*time.Millisecond)
	l.Take("a")
	if l.Stats() != 1 {
		t.Fatal("expected 1 bucket")
	}
	time.Sleep(60 * time.Millisecond)
	// 触发 GC（通过下一次 take，gcEvery=20ms 已过期）
	l.Take("trigger-gc")
	// idle 桶（a）应该被清，但 trigger-gc 还在
	stats := l.Stats()
	if stats != 1 {
		t.Errorf("Stats = %d, want 1 (only trigger-gc left)", stats)
	}
}

func TestConcurrent(t *testing.T) {
	l := NewLimiter(100, 0, time.Minute) // 100 容量，0 refil
	var wg sync.WaitGroup
	var allowed int
	var mu sync.Mutex
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := l.Take("concurrent")
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 100 {
		t.Errorf("allowed = %d, want 100 (cap=100)", allowed)
	}
}
