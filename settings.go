package main

import (
	"context"
	"os/exec"
	"slices"
	"strings"

	"androidfun/internal/adb"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) openSettings() {
	if a.settingsWindow != nil && !a.settingsWindow.IsDestroyed() {
		if a.settingsWindow.IsMinimized() {
			a.settingsWindow.Restore()
		}
		a.settingsWindow.Show()
		a.settingsWindow.Focus()
		return
	}
	window := mygo.NewWindow(mygo.WindowOptions{
		Title: a.tr("设置"), Width: 680, Height: 560, MinWidth: 640, MinHeight: 500,
		TitleBarStyle: mygo.TitleBarHidden, StateKey: "settings", Content: ui.View(a.settingsView),
	})
	a.settingsWindow = window
	window.OnClosed(func() {
		if a.settingsWindow == window {
			a.settingsWindow = nil
		}
	})
}

func (a *app) settingsView(c *ui.Context) {
	a.useLanguage(c)
	desktopTheme(c)
	t, bar := c.Theme(), c.TitleBar()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(max(52, bar.Height)).Padding(0, bar.Right+24, 0, bar.Left+24).DragWindow().AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("设置")).FontSize(18).FontWeight(600)
		})
		ui.Row(c).Padding(4, 24, 12).Justify(ui.Center).Children(func() {
			ui.Segmented(c, &a.settingsTab, a.tr("连接"), "ADB", a.tr("操作记录")).Height(32).Label(a.tr("设置分类"))
		})
		a.languageSettings(c)
		ui.Divider(c)
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			switch a.settingsTab {
			case 1:
				a.adbSettings(c)
			case 2:
				a.historySettings(c)
			default:
				a.connectionSettings(c)
			}
		})
		ui.Row(c).Height(56).Shrink(0).Padding(0, 24).Gap(12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			color := t.TextMuted
			if a.failed {
				color = t.Danger
			}
			ui.Text(c, a.tr(a.status)).FontSize(12).SingleLine().Tooltip(a.tr(a.status)).TextColor(color).Grow(1).MinWidth(0)
			if a.busy && ui.Button(c, a.tr("取消任务")).Height(36).Clicked() {
				a.cancel()
				a.status = a.tr("正在取消… 已完成的操作不会撤销。")
				a.invalidateWindows()
			}
		})
	})
}

func (a *app) connectionSettings(c *ui.Context) {
	t := c.Theme()
	locked := a.busy || a.confirmOpen
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(24).Gap(16).Children(func() {
			ui.Text(c, a.tr("设备连接")).FontSize(16).FontWeight(600)
			if a.serial == "" {
				ui.Text(c, a.tr("通过 USB 连接手机并允许 USB 调试，或在下方连接无线设备。请在主窗口选择设备。")).FontSize(13).LineHeight(1.4).TextColor(t.TextMuted)
			} else {
				ui.Text(c, a.serial).FontSize(13).Selectable().TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				options := make([]string, len(a.users))
				for i, user := range a.users {
					options[i] = a.userTitle(user)
				}
				if ui.Select(c, &a.userLabel, options).Height(40).Label(a.tr("Android 用户")).Grow(1).MinWidth(0).Disabled(locked || len(options) == 0).Changed() {
					for _, user := range a.users {
						if a.userTitle(user) == a.userLabel {
							a.userID = user.ID
							a.refreshApps()
							a.invalidateWindows()
							break
						}
					}
				}
				if ui.Button(c, a.tr("刷新设备")).Height(36).Disabled(locked).Clicked() {
					a.refreshDevices()
				}
				if adb.IsNetworkDevice(a.serial) && ui.Button(c, a.tr("断开无线设备")).Height(36).Disabled(locked).Clicked() {
					a.disconnect()
				}
			})
			ui.Divider(c)
			ui.Text(c, a.tr("无线调试")).FontSize(16).FontWeight(600)
			ui.Text(c, a.tr("在手机的开发者选项中打开无线调试。Android 11+ 首次连接需要配对；配对端口与连接端口通常不同。")).FontSize(13).LineHeight(1.4).TextColor(t.TextMuted)
			ui.Text(c, a.tr("1. 配对（已配对可跳过）")).FontSize(13).FontWeight(600)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &a.pairAddress).Height(40).Label(a.tr("配对地址")).Placeholder(a.tr("IP:配对端口")).Grow(1).MinWidth(0).Disabled(locked)
				ui.TextInput(c, &a.pairCode).Height(40).Label(a.tr("配对码")).Placeholder(a.tr("6 位配对码")).Password().Width(150).Disabled(locked)
				if ui.Button(c, a.tr("配对")).Height(36).Disabled(locked || a.client.Path == "").Clicked() {
					client, address, code := a.client, strings.TrimSpace(a.pairAddress), strings.TrimSpace(a.pairCode)
					a.pairCode = ""
					a.run(a.tr("正在配对…"), func(ctx context.Context) (func(), error) {
						if err := client.Pair(ctx, address, code); err != nil {
							return nil, err
						}
						return func() { a.status = a.tr("配对成功，请填写无线调试页面的连接端口。") }, nil
					})
				}
			})
			ui.Text(c, a.tr("2. 连接设备")).FontSize(13).FontWeight(600)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &a.address).Height(40).Label(a.tr("连接地址")).Placeholder(a.tr("IP:连接端口，例如 192.168.1.20:37123")).Grow(1).MinWidth(0).Disabled(locked)
				if ui.PrimaryButton(c, a.tr("连接")).Height(36).Disabled(locked || a.client.Path == "").Clicked() {
					client, address := a.client, strings.TrimSpace(a.address)
					previous := slices.Clone(a.devices)
					a.run(a.tr("正在连接…"), func(ctx context.Context) (func(), error) {
						if err := client.Connect(ctx, address); err != nil {
							return nil, err
						}
						devices, err := client.Devices(ctx)
						if err != nil {
							return nil, err
						}
						if err := enrichDeviceInfo(ctx, client, devices, previous); err != nil {
							return nil, err
						}
						return func() {
							a.acceptDevices(devices)
							a.status = a.tr("连接成功，请在主窗口选择设备。")
						}, nil
					})
				}
			})
			if a.client.Path == "" {
				ui.Text(c, a.tr("请先在 ADB 页面指定可用的 adb。")).FontSize(12).TextColor(t.Warning)
			}
			if a.confirmOpen {
				ui.Text(c, a.tr("请先完成或取消主窗口中的批量操作确认。")).FontSize(12).TextColor(t.TextMuted)
			}
			a.rememberedDevices(c)
		})
	})
}

func (a *app) adbSettings(c *ui.Context) {
	t := c.Theme()
	locked := a.busy || a.confirmOpen
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(24).Gap(16).Children(func() {
			ui.Text(c, a.tr("ADB 可执行文件")).FontSize(16).FontWeight(600)
			ui.Text(c, a.tr("优先使用随应用附带的 platform-tools，也支持已安装的 Android SDK 或自定义 adb。")).FontSize(13).LineHeight(1.4).TextColor(t.TextMuted)
			ui.TextInput(c, &a.adbPath).Height(40).Label(a.tr("ADB 可执行文件路径")).Placeholder(a.tr("/path/to/adb 或 C:\\platform-tools\\adb.exe")).FillWidth().Disabled(locked)
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, a.tr("应用并查找设备")).Height(36).Disabled(locked).Clicked() {
					path, err := exec.LookPath(strings.TrimSpace(a.adbPath))
					if err != nil {
						a.status, a.failed = a.tr("ADB 路径无效：")+err.Error(), true
					} else {
						a.client.Path, a.adbPath = path, path
						a.clearDevice()
						a.saveADBPath(path)
						a.refreshDevices()
					}
					a.invalidateWindows()
				}
			})
			if a.preferenceError != "" {
				ui.Text(c, a.preferenceError).TextColor(t.Danger).Selectable()
			}
			if a.confirmOpen {
				ui.Text(c, a.tr("请先完成或取消主窗口中的批量操作确认。")).FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
}

func (a *app) historySettings(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Padding(24).Gap(16).Children(func() {
		if a.historyError != nil {
			ui.Text(c, a.historyErrorText()).FontSize(12).TextColor(t.Danger).Selectable()
		}
		if len(a.history) == 0 {
			ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Children(func() {
				ui.Text(c, a.tr("暂无操作记录")).FontSize(16).FontWeight(600)
				ui.Text(c, a.tr("应用操作会自动保存在本地，重启后仍可查看。")).FontSize(13).LineHeight(1.4).TextColor(t.TextMuted).TextAlign(ui.Center)
			})
			return
		}
		ui.Textf(c, a.tr("%d 条操作记录"), len(a.history)).FontSize(13).TextColor(t.TextMuted)
		rows := groupHistory(a.history)
		ui.List(c, nil, len(rows), func(i int) {
			row := rows[i]
			if row.index < 0 {
				ui.Text(c, row.day).FontSize(13).FontWeight(600).Padding(16, 0, 8)
				return
			}
			entry := a.history[row.index]
			ui.Column(c).Gap(6).Padding(12, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, a.historyMetadata(entry)).FontSize(12).TextColor(t.TextMuted).Selectable()
				ui.Text(c, a.historyAction(entry)+" · "+entry.Package).FontSize(13).FontWeight(600).Selectable()
				color := t.Danger
				if entry.OK {
					color = t.Success
				}
				ui.Text(c, a.historyMessage(entry)).FontSize(13).LineHeight(1.4).TextColor(color).Selectable()
			})
		}).Grow(1).MinHeight(0)
	})
}
