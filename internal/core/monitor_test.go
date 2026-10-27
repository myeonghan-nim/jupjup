package core

import (
	"context"
	"testing"
	"time"
)

func TestIsBargain(t *testing.T) {
	cases := []struct {
		lowest, next, percent int64
		want                  bool
	}{
		{1, 10, 10, true},   // 10% 정확히
		{2, 10, 10, false},  // 초과
		{15, 150, 10, true}, // Python: 15 <= 15.0 (경계 포함)
		{16, 150, 10, false},
		{100, 45000, 10, true},
		{30, 150, 20, true}, // 20% 경계
		{31, 150, 20, false},
		{75, 150, 50, true}, // 50% 경계
		{76, 150, 50, false},
		{16, 150, 11, true}, // 기준 완화 → 기존 비특가가 특가로
	}
	for _, c := range cases {
		if got := IsBargain(c.lowest, c.next, c.percent); got != c.want {
			t.Errorf("IsBargain(%d, %d, %d) = %v, want %v", c.lowest, c.next, c.percent, got, c.want)
		}
	}
}

// fakeFetcher는 호출마다 미리 정한 가격을 차례로 돌려준다.
type fakeFetcher struct {
	seq [][2]int64
	i   int
	err error
}

func (f *fakeFetcher) FetchLowestTwo(ctx context.Context, keyword string) (int64, int64, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	p := f.seq[f.i%len(f.seq)]
	f.i++
	return p[0], p[1], nil
}

func newTestMonitor(f Fetcher) (*Monitor, *[]string) {
	alerts := &[]string{}
	m := &Monitor{
		Fetch:     f,
		Items:     func() []string { return []string{"마나 허브"} },
		Interval:  func() time.Duration { return time.Hour },
		Threshold: func() int64 { return 10 },
		OnLog:     func(string) {},
		OnAlert: func(item string, next, lowest int64) {
			*alerts = append(*alerts, AlertText(item, next, lowest))
		},
	}
	m.lastAlerted = map[string]int64{}
	return m, alerts
}

func TestMonitorDedup(t *testing.T) {
	// 사이클: 특가(100) → 같은 가격 유지 → 가격 변동(90) → 특가 해제 → 같은 가격 재등장(90)
	f := &fakeFetcher{seq: [][2]int64{{100, 45000}, {100, 45000}, {90, 45000}, {40000, 45000}, {90, 45000}}}
	m, alerts := newTestMonitor(f)
	for i := 0; i < 5; i++ {
		m.cycle(context.Background())
	}
	// 기대: 1번째(신규), 3번째(가격 변동), 5번째(해제 후 재등장) = 총 3회
	if len(*alerts) != 3 {
		t.Errorf("알림 %d회, want 3회: %v", len(*alerts), *alerts)
	}
}

func TestMonitorAuthErrorStopsCycle(t *testing.T) {
	f := &fakeFetcher{err: ErrUnauthorized}
	m, _ := newTestMonitor(f)
	called := false
	m.OnAuthError = func() { called = true }
	m.cycle(context.Background())
	if !called {
		t.Error("OnAuthError 미호출")
	}
}

func TestMonitorRunStopsOnCancel(t *testing.T) {
	f := &fakeFetcher{seq: [][2]int64{{40000, 45000}}}
	m, _ := newTestMonitor(f)
	m.Interval = func() time.Duration { return time.Millisecond }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run이 취소 후에도 종료되지 않음")
	}
}
