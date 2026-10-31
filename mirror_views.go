package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"androidfun/internal/scrcpy"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	mirrorGlyph  = glyph(`<path d="M5 13H3a2 2 0 0 1-2-2V3a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v1"/><rect x="6" y="6" width="13" height="12" rx="2"/>`)
	cameraGlyph  = glyph(`<path d="M3 6h3l1-2h6l1 2h3v11H3z"/><circle cx="10" cy="11" r="3"/>`)
	recordGlyph  = glyph(`<circle cx="10" cy="10" r="7"/><circle cx="10" cy="10" r="3" fill="currentColor" stroke="none"/>`)
	stopGlyph    = glyph(`<rect x="5" y="5" width="10" height="10" rx="2"/>`)
	rotateGlyph  = glyph(`<path d="M16 6a7 7 0 0 0-12 4M16 2v4h-4M4 14a7 7 0 0 0 12-4M4 18v-4h4"/>`)
	backGlyph    = glyph(`<path d="m12 4-7 6 7 6z"/>`)
	homeGlyph    = glyph(`<circle cx="10" cy="10" r="6"/>`)
	recentsGlyph = glyph(`<rect x="4" y="4" width="12" height="12" rx="2"/>`)
)

func mirrorButton(c *ui.Context, label string, icon *ui.SVG, disabled, danger bool) bool {
	color := c.Theme().Text
	if danger {
		color = c.Theme().Danger
	}
	return ui.ButtonBase(c).Size(52, 52).Radius(26).Center().Label(label).Tooltip(label).Disabled(disabled).Children(func() {
		ui.Icon(c, icon).Size(24, 24).TextColor(color)
	}).Clicked()
}

func (a *app) mirrorView(c *ui.Context, m *mirrorState) {
	a.useLanguage(c)
	desktopTheme(c)
	t, bar := c.Theme(), c.TitleBar()
	width, height := c.Size()
	headerHeight := max(float32(56), bar.Height)
	footerHeight := float32(0)
	if m.failed || m.lastFile != "" {
		footerHeight = 30
	}
	frameWidth, frameHeight := 9, 20
	if m.frame != nil {
		frameWidth, frameHeight = m.frame.Size()
	}
	m.updateGeometry(mirrorGeometry{width: frameWidth, height: frameHeight, header: int(headerHeight), footer: int(footerHeight)})
	videoWidth, videoHeight := mirrorDisplaySize(width-mirrorHorizontalInset, height-headerHeight-footerHeight-12, frameWidth, frameHeight)
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(headerHeight).FillWidth().Padding(8, bar.Right+16, 8, bar.Left+16).Gap(12).DragWindow().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, m.name).FontSize(15).FontWeight(600).SingleLine().Tooltip(m.name)
				ui.Text(c, m.metadata).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(m.metadata + " · " + m.config.Serial)
			})
			if m.recorder != nil {
				ui.Text(c, a.tr("正在录屏")).FontSize(12).TextColor(t.Danger)
			}
		})
		ui.Row(c).Grow(1).MinHeight(0).FillWidth().Padding(0, 12, 12, 12).Gap(8).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			video := ui.Column(c).Size(videoWidth, videoHeight).Shrink(0).Radius(18).Background(t.Surface).Clip().Focusable().Label(a.tr("设备屏幕"))
			video.Children(func() {
				if m.frame != nil {
					ui.Image(c, m.frame).Fill().Fit(ui.Contain)
				} else {
					ui.Column(c).Fill().Center().Padding(20).Gap(14).Children(func() {
						ui.Image(c, androidGlyph).Size(40, 40)
						color := t.TextMuted
						if m.failed {
							color = t.Danger
						}
						ui.Text(c, m.status).FontSize(13).LineHeight(1.5).TextColor(color).TextAlign(ui.Center)
					})
				}
			})
			// Event callbacks run outside the build pass; capture geometry, never a ui.Element.
			bounds := ui.Rect{W: videoWidth, H: videoHeight}
			video.HandleInput(func(event ui.InputEvent) bool { return a.mirrorInput(m, bounds, event) })
			ui.Scroll(c).Width(72).Height(videoHeight).Children(func() {
				ui.Column(c).FillWidth().Height(max(480, videoHeight)).AlignItems(ui.Center).Children(func() { a.mirrorControls(c, m) })
			})
		})
		if m.failed || m.lastFile != "" {
			color := t.TextMuted
			if m.failed {
				color = t.Danger
			}
			ui.Text(c, m.status).FontSize(12).TextColor(color).SingleLine().Tooltip(m.status+"\n"+m.lastFile).FillWidth().Height(footerHeight).Padding(0, 16, 12)
		}
	})
}

func (a *app) mirrorControls(c *ui.Context, m *mirrorState) {
	t := c.Theme()
	locked := m.busy || m.stopping || m.session == nil
	ui.Column(c).Padding(6).Gap(4).Radius(32).Background(t.Surface).Children(func() {
		if mirrorButton(c, a.tr("截屏"), cameraGlyph, locked, false) {
			a.takeScreenshot(m)
		}
		if m.recorder != nil {
			if mirrorButton(c, a.tr("停止录屏"), stopGlyph, m.recordingStopping || m.stopping, true) {
				a.stopRecording(m)
			}
			now := c.Now()
			if !m.recordingStopped.IsZero() {
				now = m.recordingStopped
			} else {
				c.After(time.Second)
			}
			ui.Text(c, recordingTime(m.recordingStarted, now)).FontSize(11).FontWeight(600).TextColor(t.Danger).FillWidth().TextAlign(ui.Center).Padding(0, 0, 6).Tooltip(a.tr("录屏时长"))
		} else if mirrorButton(c, a.tr("录屏"), recordGlyph, locked, false) {
			a.startRecording(m)
		}
		if mirrorButton(c, a.tr("旋转屏幕"), rotateGlyph, locked, false) {
			a.mirrorCommand(m, m.device.Rotate)
		}
		if mirrorButton(c, a.tr("投屏设置"), settingsGlyph, false, false) {
			a.settingsTab = 3
			a.openSettings()
		}
	})
	ui.Box(c).Grow(1).MinHeight(16)
	if m.session == nil && !m.busy && !m.stopping {
		if mirrorButton(c, a.tr("重新投屏"), refreshGlyph, !a.canMirrorDevice(m.config.Serial), false) {
			a.restartMirror(m)
		}
	}
	ui.Column(c).Padding(6).Gap(4).Radius(32).Background(t.Surface).Children(func() {
		for _, item := range []struct {
			label string
			icon  *ui.SVG
			key   scrcpy.Key
		}{
			{a.tr("最近任务"), recentsGlyph, scrcpy.KeyAppSwitch}, {"Home", homeGlyph, scrcpy.KeyHome}, {a.tr("返回"), backGlyph, scrcpy.KeyBack},
		} {
			if mirrorButton(c, item.label, item.icon, locked, false) {
				a.mirrorCommand(m, func(ctx context.Context) error { return m.device.Key(ctx, item.key) })
			}
		}
	})
}

func (a *app) mirrorSettings(c *ui.Context) {
	t := c.Theme()
	p := &a.config.Mirroring
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(24).Gap(16).Children(func() {
			ui.Text(c, a.tr("投屏")).FontSize(16).FontWeight(600)
			ui.Text(c, a.tr("通过 scrcpy 投屏并控制设备，选项在下次启动投屏时生效。")).FontSize(13).LineHeight(1.5).TextColor(t.TextMuted)
			ui.TextInput(c, &p.Path).Height(42).Label(a.tr("scrcpy 可执行文件路径")).Placeholder(a.tr("自动查找内置或已安装的 scrcpy")).FillWidth()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, a.tr("选择文件")).Height(36).Clicked() {
					a.chooseScrcpy()
				}
				if ui.Button(c, a.tr("保存设置")).Height(36).Clicked() {
					if _, err := scrcpy.Discover(p.Path); err != nil {
						a.status, a.failed = err.Error(), true
					} else {
						a.savePreferences()
						a.status, a.failed = a.tr("设置已保存"), false
						if a.preferenceError != "" {
							a.status, a.failed = a.preferenceError, true
						}
					}
					a.invalidateWindows()
				}
			})
			ui.Divider(c)
			a.captureSettings(c)
			ui.Divider(c)
			for _, option := range []struct {
				label, help string
				value       *bool
			}{
				{"保持常亮（充电时）", "对应 -w / --stay-awake；仅在设备接通电源时阻止自动休眠。", &p.StayAwake},
				{"保持活跃（包括无线连接）", "对应 --keep-active；定期向设备报告活动，避免因空闲锁屏。", &p.KeepActive},
				{"投屏时关闭手机屏幕", "对应 --turn-screen-off；电脑上的投屏仍可操作。", &p.TurnScreenOff},
				{"窗口置顶", "让投屏窗口保持在其他窗口上方。", &p.AlwaysOnTop},
				{"关闭音频", "投屏和录屏都只包含画面。", &p.NoAudio},
				{"显示触摸位置", "显示手机上的手指触摸位置，退出投屏时恢复。", &p.ShowTouches},
				{"阻止电脑屏保", "投屏期间禁止电脑进入屏幕保护程序。", &p.DisableScreensaver},
			} {
				ui.Column(c).Gap(3).Children(func() {
					bigger := *t
					bigger.Spacing = 6
					c.SetTheme(&bigger)
					changed := ui.Checkbox(c, option.value, a.tr(option.label)).MinHeight(36).Changed()
					c.SetTheme(t)
					ui.Text(c, a.tr(option.help)).FontSize(12).LineHeight(1.4).TextColor(t.TextMuted).Padding(0, 0, 4, 34)
					if changed {
						a.savePreferences()
					}
				})
			}
			ui.Divider(c)
			a.mirrorChoice(c, a.tr("最长边分辨率"), &p.MaxSize, []int{0, 720, 1080, 1280, 1920, 2560}, "px")
			a.mirrorChoice(c, a.tr("帧率上限"), &p.MaxFPS, []int{0, 15, 30, 60, 90, 120}, "fps")
			a.mirrorChoice(c, a.tr("视频码率"), &p.VideoBitRate, []int{0, 2000000, 4000000, 8000000, 16000000, 32000000}, "Mbps")
			ui.Text(c, a.tr("截图和录屏按保存设置存储。停止录屏不会关闭投屏。")).FontSize(12).LineHeight(1.5).TextColor(t.TextMuted)
			if a.preferenceError != "" {
				ui.Text(c, a.preferenceError).FontSize(12).TextColor(t.Danger)
			}
		})
	})
}

func (a *app) mirrorChoice(c *ui.Context, label string, value *int, values []int, unit string) {
	labels := make([]string, len(values))
	selected := a.tr("默认")
	for i, v := range values {
		labels[i] = a.tr("默认")
		if v > 0 {
			number := v
			if unit == "Mbps" {
				number = v / 1000000
			}
			labels[i] = strconv.Itoa(number) + " " + unit
		}
		if v == *value {
			selected = labels[i]
		}
	}
	ui.Row(c).MinHeight(48).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Grow(1)
		if ui.Select(c, &selected, labels).Width(172).Height(40).Label(label).Changed() {
			for i, s := range labels {
				if selected == s {
					*value = values[i]
					a.savePreferences()
					break
				}
			}
		}
	})
}

func (a *app) chooseScrcpy() {
	parent := a.settingsWindow
	tr := a.translator()
	a.run(a.tr("选择 scrcpy"), func(ctx context.Context) (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: tr("选择 scrcpy")})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		path, err := scrcpy.Discover(paths[0])
		if err != nil {
			return nil, fmt.Errorf("scrcpy: %w", err)
		}
		return func() { a.config.Mirroring.Path = path; a.savePreferences(); a.status = a.tr("设置已保存") }, nil
	})
}
