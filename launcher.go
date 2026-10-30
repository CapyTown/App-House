package main

import (
	"slices"
	"strings"
	"time"

	"androidfun/internal/adb"
	"androidfun/internal/preferences"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) invalidateWindows() {
	for _, win := range []*mygo.Window{a.win, a.settingsWindow} {
		if win != nil && !win.IsDestroyed() {
			win.Invalidate()
		}
	}
}

func (a *app) loadPreferences() {
	path, err := preferences.Path()
	if err != nil {
		a.preferenceError = err.Error()
		return
	}
	config, err := preferences.Load(path)
	if err != nil {
		a.preferenceError = a.tr("无法读取设置：") + err.Error()
		return
	}
	a.configPath, a.config = path, config
	a.client.Path, a.adbPath = config.ADBPath, config.ADBPath
}

func (a *app) savePreferences() {
	if a.configPath == "" {
		return
	}
	if err := preferences.Save(a.configPath, a.config); err != nil {
		a.preferenceError = a.tr("无法保存设置：") + err.Error()
	} else {
		a.preferenceError = ""
	}
}

func (a *app) saveADBPath(path string) {
	a.config.ADBPath = path
	a.savePreferences()
}

func (a *app) acceptDevices(devices []adb.Device) {
	changed := false
	for _, device := range devices {
		if device.State != "device" {
			continue
		}
		wasOnline := slices.ContainsFunc(a.devices, func(old adb.Device) bool {
			return old.Serial == device.Serial && old.State == "device" && old.Model == device.Model
		})
		remembered := slices.ContainsFunc(a.config.Devices, func(old preferences.Device) bool { return old.Serial == device.Serial })
		if !wasOnline || !remembered {
			a.config.Remember(device.Serial, device.Model, time.Now())
			changed = true
		}
	}
	a.devices = devices
	if a.serial != "" && !a.deviceOnline(a.serial) {
		a.clearDevice()
	}
	if changed {
		a.savePreferences()
	}
}

func (a *app) deviceOnline(serial string) bool {
	return slices.ContainsFunc(a.devices, func(device adb.Device) bool { return device.Serial == serial && device.State == "device" })
}

func (a *app) launcherDevices() []adb.Device {
	devices := slices.Clone(a.devices)
	for _, remembered := range a.config.Devices {
		index := slices.IndexFunc(devices, func(d adb.Device) bool { return d.Serial == remembered.Serial })
		if index < 0 {
			devices = append(devices, adb.Device{Serial: remembered.Serial, Model: remembered.Model, State: "offline"})
		} else if devices[index].Model == "" || devices[index].Model == devices[index].Serial {
			devices[index].Model = remembered.Model
		}
	}
	slices.SortStableFunc(devices, func(a, b adb.Device) int {
		if a.State == "device" && b.State != "device" {
			return -1
		}
		if b.State == "device" && a.State != "device" {
			return 1
		}
		return 0
	})
	return devices
}

func deviceName(device adb.Device) string {
	if device.Model != "" {
		return strings.ReplaceAll(device.Model, "_", " ")
	}
	return device.Serial
}

func launcherOptions(content *ui.Content) mygo.WindowOptions {
	return mygo.WindowOptions{Title: appName, Width: 480, Height: 540, DisableResize: true, DisableMaximize: true, TitleBarStyle: mygo.TitleBarHidden, Content: content}
}

func (a *app) openLauncher() {
	a.win = mygo.NewWindow(launcherOptions(ui.View(a.launcherView)))
	a.win.OnClosed(func() {
		if !a.mainStarted {
			mygo.App.Quit()
		}
	})
}

func (a *app) openMain(serial string) {
	if a.mainStarted || a.busy || !a.deviceOnline(serial) {
		if !a.deviceOnline(serial) {
			a.status = a.tr("设备离线或尚未授权，请连接并在手机上允许 USB 调试。")
		}
		return
	}
	launcher := a.win
	a.mainStarted = true
	a.win = mygo.NewWindow(mygo.WindowOptions{Title: appName, Width: 1480, Height: 900, MinWidth: 1060, MinHeight: 680, TitleBarStyle: mygo.TitleBarHidden, StateKey: "main", Content: ui.View(a.view)})
	a.win.OnClosed(func() { mygo.App.Quit() })
	a.chooseDevice(serial)
	launcher.Close()
	a.win.Show()
	a.win.Focus()
}

// Post discoveries onto the UI thread so selection and refresh cannot race.
func (a *app) watchLauncher() {
	window := a.win
	time.AfterFunc(3*time.Second, func() {
		window.Update(func() {
			if a.mainStarted || a.ctx.Err() != nil {
				return
			}
			if !a.busy && !a.confirmOpen {
				a.refreshDevices()
			}
			a.watchLauncher()
		})
	})
}

func (a *app) launcherView(c *ui.Context) {
	a.useLanguage(c)
	desktopTheme(c)
	t, bar := c.Theme(), c.TitleBar()
	devices := a.launcherDevices()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(max(48, bar.Height)).DragWindow().Padding(0, bar.Right+16, 0, bar.Left+16).Justify(ui.End).Children(func() {})
		ui.Column(c).Padding(8, 28, 24).Gap(8).Children(func() {
			ui.Text(c, a.tr("选择设备")).FontSize(26).FontWeight(700).LetterSpacing(-0.4)
			ui.Text(c, a.tr("双击在线设备，开始管理应用。")).FontSize(13).LineHeight(1.4).TextColor(t.TextMuted)
		})
		a.launcherList.Key = func(i int) any { return devices[i].Serial }
		a.launcherList.Selected = &a.launcherSelection
		a.launcherList.Label = func(i int) string { return deviceName(devices[i]) + " · " + devices[i].Serial }
		list := ui.List(c, &a.launcherList, len(devices), func(i int) {
			device := devices[i]
			text, muted := t.Text, t.TextMuted
			if i == a.launcherSelection {
				text, muted = t.AccentText, t.AccentText
			}
			row := ui.Row(c).Height(72).FillWidth().Padding(12, 16).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Image(c, androidGlyph).Size(28, 28)
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, deviceName(device)).FontSize(14).FontWeight(600).TextColor(text).SingleLine().Tooltip(deviceName(device))
					state := a.tr("离线")
					if device.State == "device" {
						state = a.tr("已连接")
					} else if device.State == "unauthorized" {
						state = a.tr("待授权")
					}
					ui.Text(c, device.Serial+" · "+state).FontSize(12.5).TextColor(muted).SingleLine().Tooltip(device.Serial + " · " + state)
				})
				color := t.TextMuted
				if device.State == "device" {
					color = t.Success
				}
				ui.Box(c).Size(8, 8).Radius(4).Background(color)
			})
			if row.RowDoubleClicked() {
				a.openMain(device.Serial)
			}
		}).Grow(1).MinHeight(0).Padding(0, 12).Disabled(a.busy)
		if len(devices) == 0 {
			list.Children(func() {
				ui.Column(c).Absolute().Top(0).Right(0).Bottom(0).Left(0).Center().Padding(28).Children(func() {
					ui.Text(c, a.tr("通过 USB 连接 Android 设备，\n或在设置中添加无线设备。")).FontSize(13).LineHeight(1.5).TextColor(t.TextMuted).TextAlign(ui.Center)
				})
			})
		}
		ui.Row(c).Height(64).Shrink(0).Padding(0, 20).Gap(12).AlignItems(ui.Center).Children(func() {
			if iconButton(c, a.tr("设置"), settingsGlyph, false).Clicked() {
				a.openSettings()
			}
			color := t.TextMuted
			if a.failed {
				color = t.Danger
			}
			ui.Text(c, a.tr(a.status)).FontSize(12).TextColor(color).SingleLine().Tooltip(a.tr(a.status)).Grow(1).MinWidth(0)
			if iconButton(c, a.tr("刷新设备"), refreshGlyph, a.busy).Clicked() {
				a.refreshDevices()
			}
		})
	})
}
