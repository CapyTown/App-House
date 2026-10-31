package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"androidfun/internal/adb"
	"github.com/egoist/mygo/ui"
)

var appFilters = []string{"全部应用", "用户应用", "系统应用", "已暂停", "已停用"}

func glyph(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

var (
	appsGlyph     = glyph(`<rect x="3" y="3" width="5" height="5" rx="1"/><rect x="12" y="3" width="5" height="5" rx="1"/><rect x="3" y="12" width="5" height="5" rx="1"/><rect x="12" y="12" width="5" height="5" rx="1"/>`)
	userAppsGlyph = glyph(`<circle cx="10" cy="6" r="3"/><path d="M4 17v-2a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v2"/>`)
	disabledGlyph = glyph(`<circle cx="10" cy="10" r="7"/><path d="m5 5 10 10"/>`)
	systemGlyph   = glyph(`<rect x="5" y="5" width="10" height="10" rx="2"/><path d="M8 1v4m4-4v4M8 15v4m4-4v4M1 8h4m-4 4h4m10-4h4m-4 4h4"/>`)
	pauseGlyph    = glyph(`<path d="M7 4v12M13 4v12" stroke-width="3"/>`)
	linkGlyph     = glyph(`<path d="M7 7l3-3a4 4 0 0 1 6 6l-3 3M7 7l-3 3a4 4 0 0 0 6 6l3-3M7 13l6-6"/>`)
	refreshGlyph  = glyph(`<path d="M16 7a6 6 0 1 0 0 6M16 3v4h-4"/>`)
	panelGlyph    = glyph(`<rect x="2" y="3" width="16" height="14" rx="2"/><path d="M12 3v14"/>`)
	closeGlyph    = glyph(`<path d="M5 5l10 10M15 5L5 15"/>`)
	chevronGlyph  = glyph(`<path d="m5 8 5 5 5-5"/>`)
	moreGlyph     = glyph(`<circle cx="4" cy="10" r="1"/><circle cx="10" cy="10" r="1"/><circle cx="16" cy="10" r="1"/>`)
)

type viewColors struct{ sidebar, inspector, stripe ui.Color }

func desktopTheme(c *ui.Context) viewColors {
	t := *c.Theme()
	t.Accent, t.AccentHover, t.AccentPressed = ui.Hex("#087fff"), ui.Hex("#0072ed"), ui.Hex("#0065d4")
	t.AccentText, t.Focus = ui.Hex("#ffffff"), t.Accent
	t.FontSize, t.Spacing, t.Radius = 14, 4, 10
	colors := viewColors{ui.Hex("#f4f4f6"), ui.Hex("#f8f8fa"), ui.Hex("#f8f8fa")}
	if t.Dark {
		colors = viewColors{ui.Hex("#1c1c1f"), ui.Hex("#1c1c1f"), ui.Hex("#1c1c1f")}
		t.Background, t.Surface, t.Border = ui.Hex("#141416"), ui.Hex("#26262a"), ui.Hex("#303035")
		t.Text, t.TextMuted = ui.Hex("#f5f5f7"), ui.Hex("#98989f")
		t.SurfaceHover, t.SurfacePressed = ui.Hex("#303035"), ui.Hex("#3b3b40")
	} else {
		t.Background, t.Surface, t.Border = ui.Hex("#ffffff"), ui.Hex("#f4f4f6"), ui.Hex("#e8e8eb")
		t.Text, t.TextMuted = ui.Hex("#1d1d1f"), ui.Hex("#77777d")
		t.SurfaceHover, t.SurfacePressed = ui.Hex("#eaeaee"), ui.Hex("#dedee3")
	}
	c.SetTheme(&t)
	return colors
}

func (a *app) setRows(rows []appRow) {
	a.rows, a.focusedRow = rows, -1
	a.detail, a.detailIcon = adb.App{}, nil
	if visible := a.visibleRows(); len(visible) > 0 {
		a.focusedRow = 0
		first := a.rows[visible[0]]
		a.detail, a.detailIcon = first.App, first.icon
	}
}

func (a *app) view(c *ui.Context) {
	a.useLanguage(c)
	colors := desktopTheme(c)
	t := c.Theme()
	root := ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(232).FillHeight().Background(colors.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() { a.sidebar(c) })
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			a.toolbar(c)
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() { a.content(c, colors) })
				if a.inspectorVisible {
					a.inspector(c, colors)
				}
			})
			a.selectionBar(c)
		})
	})
	if a.canInstall() {
		if paths := root.DroppedFiles(); len(paths) > 0 {
			a.installFiles(paths)
		}
		if root.FileDragOver() {
			root.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
				p.Fill(bounds, t.Accent.Alpha(0.12), 0)
			})
		}
	}
	a.dialogs(c)
}

func iconButton(c *ui.Context, label string, icon *ui.SVG, disabled bool) ui.Element {
	return ui.ButtonBase(c).Size(40, 40).Radius(20).Center().Label(label).Tooltip(label).Disabled(disabled).TextColor(c.Theme().TextMuted).Children(func() { ui.Icon(c, icon).Size(20, 20) })
}

func navItem(c *ui.Context, label, count string, icon *ui.SVG, selected, disabled bool) bool {
	t := c.Theme()
	bg, text, secondary := ui.Transparent, t.Text, t.TextMuted
	iconColor := t.TextMuted
	if selected {
		bg, secondary, iconColor = t.Accent.Alpha(0.1), t.Accent, t.Accent
	}
	return ui.ButtonBase(c).Key(label).FillWidth().Height(44).Padding(6, 10).Gap(12).Radius(10).Background(bg).Label(label).Disabled(disabled).Children(func() {
		ui.Icon(c, icon).Size(20, 20).TextColor(iconColor)
		ui.Text(c, label).FontWeight(500).TextColor(text).Grow(1).SingleLine()
		ui.Text(c, count).FontSize(12).FontFeatures("tnum").TextColor(secondary)
	}).Clicked()
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(max(52, c.TitleBar().Height)).DragWindow()
	ui.Column(c).Padding(8, 14, 20).Gap(10).Children(func() {
		ui.Text(c, a.tr("设备")).FontSize(12).FontWeight(600).TextColor(t.TextMuted)
		selectedSerial := a.serial
		selectedName := a.tr("选择设备")
		online := []adb.Device{}
		for _, device := range a.devices {
			if device.State == "device" {
				online = append(online, device)
				if device.Serial == selectedSerial {
					selectedName = deviceName(device)
				}
			}
		}
		if len(online) == 0 {
			selectedName = a.tr("未连接设备")
		}
		// Bind options to serials, so identically named devices remain selectable.
		selectDevice := ui.SelectBase(c, &selectedSerial)
		selectDevice.Trigger.FillWidth().Height(44).Padding(0, 14).Radius(22).Background(ui.Hex("#ffffff")).TextColor(ui.Hex("#242426")).Border(0, ui.Transparent).Label(a.tr("选择设备")).Tooltip(selectedName).Disabled(a.busy || a.confirmOpen || len(online) == 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, selectedName).SingleLine().Grow(1).MinWidth(0)
			ui.Icon(c, chevronGlyph).Size(16, 16).TextColor(t.TextMuted)
		})
		selectDevice.Popup(func(panel ui.Element) {
			panel.Padding(4).Radius(12).Background(t.Background).Border(1, t.Border).MinWidth(204)
			ui.Scroll(c).MaxHeight(320).Children(func() {
				for _, device := range online {
					item := selectDevice.Item(device.Serial).Padding(8, 10).Radius(8)
					if item.Highlighted() {
						item.Background(t.Accent).TextColor(t.AccentText)
					}
					item.Children(func() { ui.Text(c, deviceName(device)).SingleLine() })
				}
			})
		})
		if selectDevice.Trigger.Changed() {
			a.chooseDevice(selectedSerial)
		}
	})
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(8, 14).Gap(4).Children(func() {
			ui.Text(c, a.tr("应用库")).FontSize(12).FontWeight(600).TextColor(t.TextMuted).Padding(0, 10, 8)
			counts := make([]int, len(appFilters))
			for _, r := range a.rows {
				counts[0]++
				if r.System {
					counts[2]++
				} else {
					counts[1]++
				}
				if r.Suspended {
					counts[3]++
				}
				if !r.Enabled {
					counts[4]++
				}
			}
			icons := []*ui.SVG{appsGlyph, userAppsGlyph, systemGlyph, pauseGlyph, disabledGlyph}
			for i, filter := range appFilters {
				if navItem(c, a.tr(filter), fmt.Sprint(counts[i]), icons[i], a.filter == filter, a.busy) {
					a.filter = filter
				}
			}
		})
	})
	ui.Row(c).Padding(12, 14).Children(func() {
		if iconButton(c, a.tr("设置"), settingsGlyph, false).Clicked() {
			a.openSettings()
		}
	})
}

func (a *app) disconnect() {
	client, serial := a.client, a.serial
	a.run(a.tr("正在断开连接…"), func(ctx context.Context) (func(), error) {
		err := client.Disconnect(ctx, serial)
		return func() {
			if err == nil {
				a.clearDevice()
				a.devices = slices.DeleteFunc(a.devices, func(d adb.Device) bool { return d.Serial == serial })
				a.status = a.tr("已断开 ") + serial
			}
		}, err
	})
}

func (a *app) toolbar(c *ui.Context) {
	t, bar := c.Theme(), c.TitleBar()
	ui.Row(c).Height(max(88, bar.Height)).Padding(18, bar.Right+24, 18, 24).Gap(12).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Column(c).Grow(1).MinWidth(80).Gap(3).Children(func() {
			ui.Text(c, a.tr("应用")).FontSize(24).FontWeight(600)
			subtitle, color := fmt.Sprintf(a.tr("%d 个应用"), len(a.rows)), t.TextMuted
			if a.busy || a.failed {
				subtitle = a.tr(a.status)
			}
			if a.failed {
				color = t.Danger
			}
			ui.Text(c, subtitle).FontSize(12).TextColor(color).SingleLine().Tooltip(subtitle)
		})
		ui.SearchField(c, &a.query).Placeholder(a.tr("搜索")).Label(a.tr("搜索应用")).Width(220).Height(40).Radius(20).Background(t.Surface).Border(0, ui.Transparent).Disabled(a.busy)
		if ui.Button(c, a.tr("安装应用")).Height(40).Padding(0, 18).Radius(20).Tooltip(a.tr("拖入 APK 以安装")).Disabled(!a.canInstall()).Clicked() {
			a.startInstall("", true)
		}

		ui.Row(c).Gap(2).Children(func() {
			if a.busy {
				if iconButton(c, a.tr("取消任务"), closeGlyph, false).Clicked() {
					a.cancel()
					a.status = a.tr("正在取消… 已完成的操作不会撤销。")
					a.invalidateWindows()
				}
			} else if iconButton(c, a.tr("刷新应用"), refreshGlyph, len(a.users) == 0).Clicked() {
				a.refreshApps()
			}
			if iconButton(c, a.tr("显示或隐藏详情"), panelGlyph, false).Clicked() {
				a.inspectorVisible = !a.inspectorVisible
			}
		})
	})
}

func (a *app) content(c *ui.Context, colors viewColors) {
	t := c.Theme()
	visible := a.visibleRows()
	if len(a.rows) == 0 {
		ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Children(func() {
			ui.Image(c, androidGlyph).Size(36, 36)
			title, body := a.tr("选择一台 Android 设备"), a.tr("开启 USB 调试并在手机上允许此电脑。")
			if a.busy {
				title, body = a.tr("正在读取应用…"), a.tr("图标、权限与签名将在读取完成后显示。")
			} else if a.serial != "" {
				title, body = a.tr("暂无应用"), a.tr("刷新重试。")
			}
			ui.Text(c, title).FontSize(17).Bold()
			ui.Text(c, body).FontSize(12).TextColor(t.TextMuted)
		})
	} else {
		a.appList(c, visible, colors)
	}
}

func (a *app) selectionBar(c *ui.Context) {
	t := c.Theme()
	visible := a.visibleRows()
	selected, allSelected := 0, len(visible) > 0
	for _, row := range a.rows {
		if row.selected {
			selected++
		}
	}
	for _, i := range visible {
		allSelected = allSelected && a.rows[i].selected
	}
	ui.Row(c).Height(68).Shrink(0).Padding(0, 24, 0, 24+t.Space(2.5)).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		checkboxTheme := *t
		checkboxTheme.Spacing = 5
		c.SetTheme(&checkboxTheme)
		selectAll := ui.Checkbox(c, &allSelected, fmt.Sprintf(a.tr("已选 %d"), selected)).Padding(4).Label(a.tr("全选当前列表")).TextColor(t.Text).Tooltip(a.tr("包含筛选后隐藏的选中项；确认窗口会列出全部目标。")).Disabled(a.busy || len(visible) == 0)
		c.SetTheme(t)
		if selectAll.Changed() {
			for _, i := range visible {
				a.rows[i].selected = allSelected
			}
		}
		if selected > 0 && ui.Button(c, a.tr("取消")).Height(40).Padding(0, 16).Radius(20).Label(a.tr("清除选择")).Disabled(a.busy).Clicked() {
			for i := range a.rows {
				a.rows[i].selected = false
			}
		}
		ui.Box(c).Grow(1)
		if selected > 0 {
			for _, action := range []adb.Action{adb.Unsuspend, adb.Suspend} {
				if ui.Button(c, a.actionTitle(action)).Height(40).Padding(0, 18).Radius(20).Disabled(a.busy).Clicked() {
					a.prepare(action)
				}
			}
			if ui.Button(c, a.tr("卸载")).Height(40).Padding(0, 18).Radius(20).TextColor(t.Danger).Disabled(a.busy).Clicked() {
				a.prepare(adb.Uninstall)
			}
		}
	})
}

func (a *app) appList(c *ui.Context, visible []int, colors viewColors) {
	t := c.Theme()
	columns := []ui.TableColumn{
		{ID: "selection", Width: 48, Fixed: true},
		{ID: "name", Title: a.tr("名称"), MinWidth: 180, Sortable: true},
		{ID: "status", Title: a.tr("状态"), Width: 108, MinWidth: 100, Sortable: true},
		{ID: "package", Title: "Package name", MinWidth: 180, Sortable: true},
		{ID: "signer", Title: a.tr("签名主体"), Width: 144, MinWidth: 110, Sortable: true},
		{ID: "permissions", Title: a.tr("权限"), Width: 112, MinWidth: 110, Sortable: true, Align: ui.End},
	}
	a.list.Key = func(i int) any { return a.rows[visible[i]].PackageName }
	a.list.Selected = &a.focusedRow
	a.list.Label = func(i int) string { return a.rows[visible[i]].Name }
	a.list.Sort = &a.sortOrder
	a.list.KeepOffset = true
	a.list.RoundRows = true
	a.list.CellMenu = nil
	appMenu := func(m *ui.Menu, index int) {
		r := a.rows[visible[index]]
		count := 1
		if r.selected {
			count = 0
			for _, row := range a.rows {
				if row.selected {
					count++
				}
			}
		}
		for _, action := range []adb.Action{adb.Unsuspend, adb.Suspend, adb.Uninstall} {
			if action == adb.Uninstall {
				m.Separator()
			}
			label := a.actionTitle(action)
			if count > 1 {
				label = fmt.Sprintf("%s (%d)", label, count)
			}
			if m.Item(label).Disabled(a.busy || a.confirmOpen || !a.deviceOnline(a.serial)).Chosen() {
				if r.selected {
					a.prepare(action)
				} else {
					a.prepare(action, r.App)
				}
			}
		}
	}
	previousSort := a.sortOrder
	var stripes []ui.Element
	table := ui.Table(c, &a.list, columns, len(visible), func(index, column int) {
		r := &a.rows[visible[index]]
		text, muted := t.Text, t.TextMuted
		if index == a.focusedRow {
			text, muted = t.AccentText, ui.Hex("#dcecff")
		}
		cell := ui.Row(c).FillWidth().Height(42).Gap(12).AlignItems(ui.Center)
		cell.PopoverMenu(func(m *ui.Menu) { appMenu(m, index) })
		if column == 0 && index%2 == 1 {
			stripes = append(stripes, cell)
		}
		cell.Children(func() {
			switch column {
			case 0:
				checkboxTheme := *t
				checkboxTheme.Spacing = 5
				c.SetTheme(&checkboxTheme)
				ui.Checkbox(c, &r.selected, "").Padding(4).Label(a.tr("选择 ") + r.PackageName).Disabled(a.busy)
				c.SetTheme(t)
			case 1:
				appIcon(c, r.icon, 30)
				ui.Text(c, r.Name).FontSize(14).FontWeight(500).Grow(1).SingleLine().TextColor(text).Tooltip(r.Name)
			case 2:
				state, color := a.appStatus(r.App), muted
				if r.Suspended || !r.Enabled {
					color = t.Danger
				}
				label := ui.Text(c, state).SingleLine().FontSize(12).TextColor(color).Tooltip(state)
				if r.Suspended || !r.Enabled {
					label.FontWeight(600)
					if index == a.focusedRow {
						label.Padding(3, 7).Radius(6).Background(ui.Hex("#ffffff"))
					}
				}
			case 3:
				ui.Text(c, r.PackageName).Grow(1).SingleLine().FontSize(12.5).TextColor(muted).Tooltip(r.PackageName)
			case 4:
				ui.Text(c, a.signerName(r.App)).Grow(1).SingleLine().FontSize(12.5).TextColor(muted).Tooltip(a.signerName(r.App))
			case 5:
				granted := 0
				for _, permission := range r.Permissions {
					if permission.Granted {
						granted++
					}
				}
				cell.Justify(ui.End)
				ui.Textf(c, "%d / %d", granted, len(r.Permissions)).FontSize(12.5).FontFeatures("tnum").TextColor(muted).Tooltip(a.tr("已授权 / 申请权限"))
			}
		})
	}).Grow(1).MinHeight(0).Margin(0, 24, 12).Disabled(a.busy)
	// Paint beneath native row selection and hover. The fixed-height cells
	// expose their row bounds without depending on private table nodes.
	table.Draw(func(p *ui.Painter, bounds ui.Rect) {
		top, bottom := bounds.Y+t.Space(8)+1, bounds.Y+bounds.H
		for _, cell := range stripes {
			row := cell.Bounds()
			y := max(top, row.Y-t.Space(1.5))
			end := min(bottom, row.Y+row.H+t.Space(1.5))
			if end > y {
				p.Fill(ui.Rect{X: bounds.X, Y: y, W: bounds.W, H: end - y}, colors.stripe, 8)
			}
		}
	})
	submitted := table.Submitted()
	if (table.Changed() || submitted) && a.sortOrder == previousSort && a.focusedRow >= 0 && a.focusedRow < len(visible) {
		r := a.rows[visible[a.focusedRow]]
		a.detail, a.detailIcon, a.permissionQuery = r.App, r.icon, ""
		if submitted {
			a.inspectorVisible = true
		}
	}
	if len(visible) == 0 {
		table.Children(func() { ui.Text(c, a.tr("没有匹配的应用")).Padding(24).TextColor(t.TextMuted) })
	}
}

func (a *app) inspector(c *ui.Context, colors viewColors) {
	t, p := c.Theme(), a.detail
	ui.Column(c).Width(312).FillHeight().Background(colors.inspector).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		if p.PackageName == "" {
			ui.Column(c).Fill().Center().Gap(12).Padding(24).Children(func() {
				ui.Icon(c, appsGlyph).Size(32, 32).TextColor(t.TextMuted)
				ui.Text(c, a.tr("应用详情")).Bold()
				ui.Text(c, a.tr("双击应用行，查看权限、\n签名和安装信息。")).TextColor(t.TextMuted).TextAlign(ui.Center)
			})
			return
		}
		ui.Row(c).Padding(24, 20, 20).Gap(12).AlignItems(ui.Center).Children(func() {
			appIcon(c, a.detailIcon, 44)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, p.Name).FontSize(18).FontWeight(600).SingleLine().Tooltip(p.Name)
				ui.Text(c, p.Version).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			if iconButton(c, a.tr("隐藏详情"), closeGlyph, false).Clicked() {
				a.inspectorVisible = false
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(0, 20, 24).Gap(24).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, a.tr("查看全部权限")).Height(38).Grow(1).Clicked() {
						a.detailOpen = true
					}
					if ui.Button(c, a.tr("复制包名")).Height(38).Clicked() {
						c.Services().WriteClipboard(p.PackageName)
					}
				})
				if p.System {
					ui.Text(c, a.tr("系统应用\n卸载或暂停可能影响系统功能。")).Padding(12).Radius(9).FontSize(12).Background(t.Warning.Alpha(0.1)).TextColor(t.Warning)
				}
				granted := 0
				for _, permission := range p.Permissions {
					if permission.Granted {
						granted++
					}
				}
				metricCard(c, a.tr("权限"), fmt.Sprintf(a.tr("%d / %d 已授权"), granted, len(p.Permissions)), t.Accent)
				ui.Column(c).Gap(10).Children(func() {
					inspectorTitle(c, a.tr("详细信息"))
					kind := a.tr("用户应用")
					if p.System {
						kind = a.tr("系统应用")
					}
					state := a.tr("正常")
					if !p.Enabled {
						state = a.tr("已停用")
					}
					if p.Suspended {
						state = a.tr("已暂停")
					}
					property(c, a.tr("类型"), kind)
					property(c, a.tr("状态"), state)
					property(c, a.tr("Android 用户"), fmt.Sprint(a.userID))
					ui.Text(c, p.PackageName).FontSize(12).Selectable().Padding(10).Radius(7).Background(t.Surface)
				})
				ui.Column(c).Gap(9).Children(func() {
					inspectorTitle(c, a.tr("签名主体"))
					ui.Text(c, a.signerName(p)).Bold().Selectable()
					ui.Text(c, a.tr("证书声明的作者 / 公司，未经身份认证。")).FontSize(12).TextColor(t.TextMuted)
					for _, signer := range p.Signers {
						ui.Text(c, signer.Subject).FontSize(12).TextColor(t.TextMuted).Selectable()
						ui.Text(c, "SHA-256").FontSize(10).Bold().TextColor(t.TextMuted)
						ui.Text(c, signer.SHA256).FontSize(10).Selectable().Padding(10).Radius(7).Background(t.Surface)
					}
				})
				ui.Column(c).Gap(8).Children(func() {
					inspectorTitle(c, a.tr("权限预览"))
					for _, permission := range p.Permissions[:min(6, len(p.Permissions))] {
						name := strings.TrimPrefix(permission.Name, "android.permission.")
						state := a.tr("未授权")
						if permission.Granted {
							state = a.tr("已授权")
						}
						property(c, name, state)
					}
					if len(p.Permissions) == 0 {
						ui.Text(c, a.tr("未申请权限")).FontSize(12).TextColor(t.TextMuted)
					}
				})
				for _, warning := range p.Warnings {
					ui.Text(c, warning).FontSize(11).TextColor(t.Warning)
				}
			})
		})
	})
}

func inspectorTitle(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(c.Theme().TextMuted)
}
func property(c *ui.Context, key, value string) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, key).FontSize(12).Grow(1).SingleLine().TextColor(c.Theme().TextMuted).Tooltip(key)
		ui.Text(c, value).FontSize(13).SingleLine()
	})
}

func metricCard(c *ui.Context, title, value string, color ui.Color) {
	ui.Row(c).Padding(14).Radius(10).Background(color.Alpha(0.08)).Children(func() {
		ui.Text(c, title).Bold().TextColor(color).Grow(1)
		ui.Text(c, value).FontSize(12).Bold()
	})
}

func appIcon(c *ui.Context, src ui.ImageSource, size float32) {
	if src != nil {
		ui.Image(c, src).Size(size, size)
	} else {
		ui.Box(c).Size(size, size).Radius(max(4, size/5)).Background(c.Theme().Surface).Center().Children(func() { ui.Icon(c, appsGlyph).Size(size*0.64, size*0.64).TextColor(c.Theme().TextMuted) })
	}
}
