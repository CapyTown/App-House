package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"androidfun/internal/preferences"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func captureDirectory(options preferences.MirrorOptions) (string, error) {
	if options.CaptureDirectory != "" {
		return options.CaptureDirectory, nil
	}
	return mygo.App.Path(mygo.PathDownloads)
}

func captureTarget(options preferences.MirrorOptions, kind, extension string, dialog mygo.SaveDialogOptions) (string, error) {
	dir, err := captureDirectory(options)
	if err != nil {
		return "", err
	}
	dialog.DefaultPath = filepath.Join(dir, fmt.Sprintf("App House %s %s%s", kind, time.Now().Format("2006-01-02 15-04-05.000"), extension))
	if options.AskCaptureLocation {
		return mygo.Dialog.Save(dialog)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create capture directory: %w", err)
	}
	// Screenshot and recording writers use exclusive creation, preserving files
	// even if a generated name happens to collide with an existing capture.
	return dialog.DefaultPath, nil
}

func recordingTime(start, now time.Time) string {
	seconds := 0
	if !start.IsZero() {
		seconds = max(0, int(now.Sub(start)/time.Second))
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func (a *app) captureSettings(c *ui.Context) {
	p, t := &a.config.Mirroring, c.Theme()
	ui.Text(c, a.tr("截图与录屏保存")).FontSize(16).FontWeight(600)
	dir, _ := captureDirectory(*p)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, dir).FontSize(13).TextColor(t.TextMuted).SingleLine().Tooltip(dir).Grow(1).MinWidth(0)
		if ui.Button(c, a.tr("更改目录")).Height(36).Clicked() {
			a.chooseCaptureDirectory()
		}
		if ui.Button(c, "Downloads").Height(36).Disabled(p.CaptureDirectory == "").Clicked() {
			p.CaptureDirectory = ""
			a.savePreferences()
		}
	})
	bigger := *t
	bigger.Spacing = 6
	c.SetTheme(&bigger)
	if ui.Checkbox(c, &p.AskCaptureLocation, a.tr("每次询问保存位置")).MinHeight(36).Changed() {
		a.savePreferences()
	}
	c.SetTheme(t)
}

func (a *app) chooseCaptureDirectory() {
	parent, tr := a.settingsWindow, a.translator()
	dir, _ := captureDirectory(a.config.Mirroring)
	a.run(a.tr("选择保存目录"), func(context.Context) (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: tr("选择保存目录"), DefaultPath: dir, Directory: true, CreateDirectories: true})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() {
			a.config.Mirroring.CaptureDirectory = paths[0]
			a.savePreferences()
		}, nil
	})
}
