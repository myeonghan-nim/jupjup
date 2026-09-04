package main

import (
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

//go:embed winres/icon.png
var iconPNG []byte

var version = "dev" // 빌드 시 -ldflags "-X main.version=..." 주입

func main() {
	if f := setupLogFile(); f != nil {
		defer f.Close()
	}
	a := app.NewWithID("jupjup")
	a.SetIcon(fyne.NewStaticResource("icon.png", iconPNG))
	newUI(a).run()
}

// -H windowsgui 빌드는 콘솔이 없으므로 로그를 %APPDATA% 파일에도 남긴다.
func setupLogFile() *os.File {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	dir = filepath.Join(dir, "jupjup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	f, err := os.Create(filepath.Join(dir, "app.log")) // ponytail: 세션마다 새로 씀, 로테이션은 필요해지면
	if err != nil {
		return nil
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return f
}
