package core

import (
	"context"
	"errors"
	"time"
)

// IsBargain은 특가 조건(최저가 ≤ 차순위가의 percent%)을 정수 연산으로 판정한다.
func IsBargain(lowest, next, percent int64) bool {
	return lowest*100 <= next*percent
}

// Fetcher는 아이템의 (최저가, 차순위가)를 가져온다. *Client가 구현한다.
type Fetcher interface {
	FetchLowestTwo(ctx context.Context, keyword string) (lowest, next int64, err error)
}

// Monitor는 주기적으로 가격을 조회하고 중복 없이 알림을 발생시킨다.
// 콜백은 모두 Run 고루틴에서 호출된다 — UI 갱신은 호출자가 fyne.Do로 감쌀 것.
type Monitor struct {
	Fetch       Fetcher
	Items       func() []string      // 매 사이클 호출 (GUI 편집은 다음 사이클부터 반영)
	Interval    func() time.Duration // 매 사이클 호출 (설정 변경 즉시 반영)
	Threshold   func() int64         // 특가 기준 % — 매 사이클 호출
	OnLog       func(msg string)
	OnAlert     func(item string, next, lowest int64)
	OnAuthError func() // 401/403 — 호출자가 감시를 중단해야 한다

	lastAlerted map[string]int64 // 아이템 → 마지막으로 알린 최저가
}

// Run은 ctx 취소까지 블로킹한다. 작업 후 대기 방식이라 사이클이 주기보다
// 오래 걸려도 API를 연달아 치지 않는다.
func (m *Monitor) Run(ctx context.Context) {
	m.lastAlerted = map[string]int64{}
	for {
		m.cycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.Interval()):
		}
	}
}

func (m *Monitor) cycle(ctx context.Context) {
	for _, item := range m.Items() {
		if ctx.Err() != nil {
			return
		}
		lowest, next, err := m.Fetch.FetchLowestTwo(ctx, item)
		if errors.Is(err, ErrUnauthorized) {
			m.OnLog("API 키 인증 실패 — 설정에서 키를 확인해주세요")
			if m.OnAuthError != nil {
				m.OnAuthError()
			}
			return
		}
		if err != nil {
			m.OnLog(item + " 조회 실패: " + err.Error())
			continue
		}
		m.OnLog(item + " → 일반: " + Comma(next) + ", 최저: " + Comma(lowest))
		m.checkAlert(item, lowest, next)
	}
}

func (m *Monitor) checkAlert(item string, lowest, next int64) {
	if !IsBargain(lowest, next, m.Threshold()) {
		delete(m.lastAlerted, item) // 특가 해제 → 재무장
		return
	}
	if last, ok := m.lastAlerted[item]; ok && last == lowest {
		return // 같은 특가 유지 중 — 중복 알림 방지
	}
	m.lastAlerted[item] = lowest
	m.OnAlert(item, next, lowest)
}
