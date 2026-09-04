package main

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/zalando/go-keyring"

	"jupjup/internal/core"
)

const (
	keyringService  = "jupjup"
	keyringUser     = "nexon-api-key"
	prefItems       = "items"
	prefInterval    = "intervalSeconds"
	defaultInterval = 60
	maxLogLines     = 500

	statusRunning = "상태: 감시 중"
	statusStopped = "상태: 중지됨"
	statusNoKey   = "상태: 키 없음"
)

type ui struct {
	app      fyne.App
	win      fyne.Window
	status   *widget.Label
	toggle   *widget.Button
	list     *widget.List
	logLabel *widget.Label
	logView  *container.Scroll
	logLines []string
	items    []string

	cancel context.CancelFunc // nil이면 감시 중지 상태
}

func newUI(a fyne.App) *ui {
	u := &ui{app: a}
	u.win = a.NewWindow("줍줍 알리미 " + version)
	u.items = a.Preferences().StringListWithFallback(prefItems, nil)

	u.status = widget.NewLabel(statusStopped)
	u.toggle = widget.NewButton("감시 시작", u.toggleMonitoring)
	settingsBtn := widget.NewButton("설정", func() { u.showSettings(false) })

	addEntry := widget.NewEntry()
	addEntry.PlaceHolder = "모니터링할 아이템 이름"
	addBtn := widget.NewButton("추가", func() {
		u.addItem(addEntry.Text)
		addEntry.SetText("")
	})
	addEntry.OnSubmitted = func(s string) {
		u.addItem(s)
		addEntry.SetText("")
	}

	u.list = widget.NewList(
		func() int { return len(u.items) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, widget.NewButton("제거", nil), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*fyne.Container)
			name := u.items[i]
			row.Objects[0].(*widget.Label).SetText(name)
			row.Objects[1].(*widget.Button).OnTapped = func() { u.removeItem(name) }
		},
	)

	u.logLabel = widget.NewLabel("")
	u.logLabel.Wrapping = fyne.TextWrapWord
	u.logView = container.NewScroll(u.logLabel)

	topBar := container.NewHBox(u.status, layout.NewSpacer(), u.toggle, settingsBtn)
	itemsPanel := container.NewBorder(container.NewBorder(nil, nil, nil, addBtn, addEntry), nil, nil, nil, u.list)
	split := container.NewHSplit(itemsPanel, u.logView)
	split.SetOffset(0.4)
	u.win.SetContent(container.NewBorder(topBar, nil, nil, nil, split))
	u.win.Resize(fyne.NewSize(720, 460))

	u.setupTray()
	return u
}

func (u *ui) run() {
	_, err := keyring.Get(keyringService, keyringUser)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		u.status.SetText(statusNoKey)
		u.appendLog("첫 실행입니다 — 넥슨 API 키를 입력해주세요")
		u.showSettings(true)
	case err != nil:
		dialog.ShowError(errors.New("자격 증명 관리자 오류: "+err.Error()), u.win)
	default:
		u.startMonitoring()
	}
	u.win.ShowAndRun()
}

// 창 닫기(X)는 숨김 처리하고 감시는 트레이에서 계속된다. 종료는 트레이 메뉴로.
func (u *ui) setupTray() {
	desk, ok := u.app.(desktop.App)
	if !ok {
		return
	}
	quit := fyne.NewMenuItem("종료", nil) // IsQuit — Fyne이 영어 "Quit"을 자동 추가하지 않게 한국어로 명시
	quit.IsQuit = true
	desk.SetSystemTrayMenu(fyne.NewMenu("줍줍 알리미",
		fyne.NewMenuItem("열기", func() { u.win.Show() }),
		fyne.NewMenuItemSeparator(),
		quit,
	))
	u.win.SetCloseIntercept(func() { u.win.Hide() })
}

func (u *ui) toggleMonitoring() {
	if u.cancel != nil {
		u.stopMonitoring()
	} else {
		u.startMonitoring()
	}
}

func (u *ui) startMonitoring() {
	if u.cancel != nil {
		return
	}
	key, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		u.status.SetText(statusNoKey)
		u.appendLog("API 키가 없습니다 — 설정에서 입력해주세요")
		u.showSettings(true)
		return
	}
	if len(u.items) == 0 {
		u.appendLog("모니터링할 아이템이 없습니다 — 아이템을 추가한 뒤 감시를 시작하세요")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	mon := &core.Monitor{
		Fetch: core.NewClient(key),
		Items: func() []string {
			// 방어 복사 — Preferences에 저장된 슬라이스는 UI 스레드와 공유된다
			return append([]string(nil), u.app.Preferences().StringListWithFallback(prefItems, nil)...)
		},
		Interval: func() time.Duration {
			return time.Duration(u.app.Preferences().IntWithFallback(prefInterval, defaultInterval)) * time.Second
		},
		OnLog: func(msg string) {
			fyne.Do(func() { u.appendLog(msg) })
		},
		OnAlert: func(item string, next, lowest int64) {
			text := core.AlertText(item, next, lowest)
			u.app.SendNotification(fyne.NewNotification("줍줍 알리미", text))
			fyne.Do(func() { u.appendLog(text) })
		},
		OnAuthError: func() {
			fyne.Do(func() {
				if ctx.Err() != nil {
					return // 이미 중지/재시작된 감시의 지연 콜백 — 무시
				}
				u.stopMonitoring()
				u.showSettings(true)
			})
		},
	}
	go mon.Run(ctx)
	u.status.SetText(statusRunning)
	u.toggle.SetText("감시 중지")
	u.appendLog("가격 감시 시작")
}

func (u *ui) stopMonitoring() {
	if u.cancel == nil {
		return
	}
	u.cancel()
	u.cancel = nil
	u.status.SetText(statusStopped)
	u.toggle.SetText("감시 시작")
	u.appendLog("가격 감시 중지")
}

func (u *ui) addItem(name string) {
	name, err := core.ValidateItemName(name, u.items)
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	// Preferences에 게시된 배열은 감시 고루틴도 읽는다 — 제자리 수정 금지, 항상 새 배열
	items := make([]string, 0, len(u.items)+1)
	items = append(items, u.items...)
	u.items = append(items, name)
	u.app.Preferences().SetStringList(prefItems, u.items)
	u.list.Refresh()
	u.appendLog(name + " 추가됨")
}

func (u *ui) removeItem(name string) {
	kept := make([]string, 0, len(u.items)) // 공유 배열 제자리 수정 금지 (데이터 레이스 방지)
	for _, it := range u.items {
		if it != name {
			kept = append(kept, it)
		}
	}
	u.items = kept
	u.app.Preferences().SetStringList(prefItems, u.items)
	u.list.Refresh()
	u.appendLog(name + " 제거됨")
}

// restartIfRunning은 설정 저장 후 새 키/주기를 반영한다.
func (u *ui) restartIfRunning() {
	if u.cancel != nil {
		u.stopMonitoring()
		u.startMonitoring()
	}
}

func (u *ui) showSettings(startAfterSave bool) {
	keyEntry := widget.NewPasswordEntry()
	_, keyErr := keyring.Get(keyringService, keyringUser)
	hasKey := keyErr == nil
	if hasKey {
		keyEntry.PlaceHolder = "(저장됨 — 변경할 때만 입력)"
	}
	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(u.app.Preferences().IntWithFallback(prefInterval, defaultInterval)))
	// Validator가 저장 버튼을 막는다 — 폼 다이얼로그는 콜백 전에 닫히므로 사후 검증으로는 늦다
	intervalEntry.Validator = func(s string) error {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			return errors.New("1 이상의 정수를 입력해주세요")
		}
		return nil
	}

	formItems := []*widget.FormItem{
		widget.NewFormItem("넥슨 API 키", keyEntry),
		widget.NewFormItem("폴링 주기(초)", intervalEntry),
	}
	var deleteCheck *widget.Check
	if hasKey {
		deleteCheck = widget.NewCheck("저장된 API 키 삭제", nil)
		formItems = append(formItems, widget.NewFormItem("", deleteCheck))
	}

	d := dialog.NewForm("설정", "저장", "취소", formItems, func(ok bool) {
		if !ok {
			return
		}
		if n, err := strconv.Atoi(strings.TrimSpace(intervalEntry.Text)); err == nil && n >= 1 {
			u.app.Preferences().SetInt(prefInterval, n)
		}
		if deleteCheck != nil && deleteCheck.Checked {
			if err := keyring.Delete(keyringService, keyringUser); err != nil {
				dialog.ShowError(errors.New("키 삭제 실패: "+err.Error()), u.win)
				return
			}
			u.stopMonitoring()
			u.status.SetText(statusNoKey)
			u.appendLog("API 키 삭제됨")
			return
		}
		if v := strings.TrimSpace(keyEntry.Text); v != "" {
			if err := keyring.Set(keyringService, keyringUser, v); err != nil {
				dialog.ShowError(errors.New("키 저장 실패: "+err.Error()), u.win)
				return
			}
			u.appendLog("API 키 저장됨 (Windows 자격 증명 관리자)")
		}
		if startAfterSave && u.cancel == nil {
			u.startMonitoring()
		} else {
			u.restartIfRunning()
		}
	}, u.win)
	d.Resize(fyne.NewSize(440, 0))
	d.Show()
}

func (u *ui) appendLog(msg string) {
	line := time.Now().Format("15:04:05") + " " + msg
	log.Println(msg) // 파일 로그
	u.logLines = append(u.logLines, line)
	if len(u.logLines) > maxLogLines {
		u.logLines = u.logLines[len(u.logLines)-maxLogLines:]
	}
	u.logLabel.SetText(strings.Join(u.logLines, "\n"))
	u.logView.ScrollToBottom()
}
