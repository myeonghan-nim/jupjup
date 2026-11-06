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

// 자동 주기의 하한(초). 한도가 넉넉한 서비스 단계 키라도 이보다 자주 조회하지 않는다.
const minAutoSeconds = 60

// PollInterval은 폴링 주기를 정한다. seconds가 1 이상이면 그 값을 쓰고, 지정되지 않았으면(0 이하)
// 아이템 수 × 하루 사이클 수가 일일 한도(perDay)를 넘지 않는 가장 짧은 주기(초 단위 올림)를 돌려준다.
func PollInterval(seconds, itemCount, perDay int) time.Duration {
	if seconds <= 0 {
		seconds = max(minAutoSeconds, (itemCount*86400+perDay-1)/perDay)
	}
	return time.Duration(seconds) * time.Second
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
	Gap         time.Duration        // 한 사이클 안의 요청 사이 대기 (초당 한도 보호)
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
	for i, item := range m.Items() {
		if i > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(m.Gap):
			}
		}
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
