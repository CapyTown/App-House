package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"slices"
	"strings"

	"androidfun/internal/adb"
	"androidfun/internal/history"
	"androidfun/internal/preferences"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type appRow struct {
	adb.App
	selected bool
	icon     ui.ImageSource
}

type batch struct {
	serial string
	user   int
	action adb.Action
	apps   []adb.App
}

type operationResult struct {
	Package, Action, Message string
	OK                       bool
	Applied                  []adb.Action
}

const appName = "App House"

type app struct {
	locale                                  string
	settingsWindow                          *mygo.Window
	settingsTab                             int
	preferenceError, configPath             string
	config                                  preferences.Config
	mainStarted                             bool
	launcherSelection                       int
	launcherList                            ui.ListState
	sortOrder                               ui.SortOrder
	win                                     *mygo.Window
	ctx                                     context.Context
	client                                  adb.Client
	devices                                 []adb.Device
	users                                   []adb.User
	serial, userLabel                       string
	userID                                  int
	rows                                    []appRow
	list                                    ui.ListState
	query, filter                           string
	busy                                    bool
	cancel                                  context.CancelFunc
	status                                  string
	failed                                  bool
	pending                                 batch
	confirmOpen, resultsOpen, detailOpen    bool
	detail                                  adb.App
	detailIcon                              ui.ImageSource
	permissionQuery                         string
	results                                 []operationResult
	history                                 []history.Entry
	historyPath                             string
	historyError                            error
	focusedRow                              int
	inspectorVisible                        bool
	resultSummary                           string
	address, pairAddress, pairCode, adbPath string
}

func newApp() *app {
	return &app{locale: "en", ctx: context.Background(), launcherSelection: -1, sortOrder: ui.SortOrder{Column: "name"}, filter: "全部应用", focusedRow: -1, status: "连接 Android 设备以开始。"}
}

// All model changes happen on the UI thread; workers receive immutable inputs.
func (a *app) run(label string, work func(context.Context) (func(), error)) {
	if a.busy {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.busy, a.failed, a.status, a.cancel = true, false, label, cancel
	a.invalidateWindows()
	win := a.win
	go func() {
		defer cancel()
		apply, err := work(ctx)
		win.Update(func() {
			a.busy, a.cancel = false, nil
			if apply != nil {
				apply()
			}
			if err != nil {
				a.status, a.failed = a.tr(err.Error()), true
			}
			a.invalidateWindows()
		})
	}()
}

func (a *app) refreshDevices() {
	client := a.client
	a.run(a.tr("正在查找设备…"), func(ctx context.Context) (func(), error) {
		if client.Path == "" {
			path, err := adb.Discover()
			if err != nil {
				return nil, err
			}
			client.Path = path
		}
		devices, err := client.Devices(ctx)
		if err != nil {
			return nil, err
		}
		return func() {
			a.client, a.adbPath = client, client.Path
			a.acceptDevices(devices)
			a.status = fmt.Sprintf(a.tr("发现 %d 台设备。选择设备以读取应用。"), len(devices))
		}, err
	})
}

func (a *app) clearDevice() {
	a.serial, a.userLabel, a.users, a.rows = "", "", nil, nil
	a.list = ui.ListState{Columns: a.list.Columns}
	a.detailOpen, a.confirmOpen = false, false
	a.detail, a.detailIcon, a.focusedRow = adb.App{}, nil, -1
}

func (a *app) chooseDevice(serial string) {
	a.clearDevice()
	a.serial = serial
	client := a.client
	a.run(a.tr("正在读取 Android 用户…"), func(ctx context.Context) (func(), error) {
		users, err := client.Users(ctx, serial)
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			return nil, fmt.Errorf("设备没有返回可访问的 Android 用户")
		}
		user := users[0]
		for _, u := range users {
			if u.ID == 0 {
				user = u
				break
			}
		}
		apps, err := client.Apps(ctx, serial, user.ID)
		rows := makeRows(apps)
		return func() {
			a.users, a.userID, a.userLabel = users, user.ID, a.userTitle(user)
			a.setRows(rows)
			a.status = fmt.Sprintf(a.tr("已读取 %d 个应用 · 用户 %d"), len(rows), user.ID)
		}, err
	})
}

func makeRows(apps []adb.App) []appRow {
	rows := make([]appRow, len(apps))
	for i, p := range apps {
		rows[i].App = p
		if data, err := base64.StdEncoding.DecodeString(p.Icon); err == nil && len(data) > 0 {
			rows[i].icon, _ = ui.DecodeImage(data)
		}
	}
	return rows
}

func (a *app) refreshApps() {
	if a.serial == "" || len(a.users) == 0 {
		return
	}
	client, serial, user := a.client, a.serial, a.userID
	a.rows, a.list = nil, ui.ListState{Columns: a.list.Columns}
	a.detail, a.detailIcon, a.focusedRow = adb.App{}, nil, -1
	a.detailOpen = false
	a.run(a.tr("正在读取应用、图标与权限…"), func(ctx context.Context) (func(), error) {
		apps, err := client.Apps(ctx, serial, user)
		rows := makeRows(apps)
		return func() {
			a.setRows(rows)
			a.status = fmt.Sprintf(a.tr("已读取 %d 个应用 · 用户 %d"), len(rows), user)
		}, err
	})
}

func (a *app) userTitle(u adb.User) string { return fmt.Sprintf(a.tr("%s · 用户 %d"), u.Name, u.ID) }

func (a *app) visibleRows() []int {
	q := strings.ToLower(strings.TrimSpace(a.query))
	var rows []int
	for i, r := range a.rows {
		if a.filter == "用户应用" && r.System || a.filter == "系统应用" && !r.System || a.filter == "已暂停" && !r.Suspended || a.filter == "已停用" && r.Enabled {
			continue
		}
		if q == "" {
			rows = append(rows, i)
			continue
		}
		text := r.Name + " " + r.PackageName + " " + a.signerName(r.App)
		for _, p := range r.Permissions {
			text += " " + p.Name
		}
		if strings.Contains(strings.ToLower(text), q) {
			rows = append(rows, i)
		}
	}
	slices.SortStableFunc(rows, func(i, j int) int {
		x, y := a.rows[i].App, a.rows[j].App
		var result int
		switch a.sortOrder.Column {
		case "package":
			result = strings.Compare(x.PackageName, y.PackageName)
		case "signer":
			result = strings.Compare(strings.ToLower(a.signerName(x)), strings.ToLower(a.signerName(y)))
		case "status":
			result = strings.Compare(a.appStatus(x), a.appStatus(y))
		case "permissions":
			result = cmp.Compare(len(x.Permissions), len(y.Permissions))
		default:
			result = strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		}
		if result == 0 {
			result = strings.Compare(x.PackageName, y.PackageName)
		}
		if a.sortOrder.Descending {
			return -result
		}
		return result
	})
	return rows
}

func (a *app) appStatus(p adb.App) string {
	if p.Suspended && !p.Enabled {
		return a.tr("已暂停 / 已停用")
	}
	if p.Suspended {
		return a.tr("已暂停")
	}
	if !p.Enabled {
		return a.tr("已停用")
	}
	if p.System {
		return a.tr("系统应用")
	}
	return a.tr("用户应用")
}

func (a *app) signerName(p adb.App) string {
	var names []string
	for _, s := range p.Signers {
		name := s.Organization
		if name == "" {
			name = s.Subject
		}
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return a.tr("未知签名主体")
	}
	return strings.Join(names, "; ")
}

func (a *app) actionTitle(action adb.Action) string {
	switch action {
	case adb.Uninstall:
		return a.tr("卸载")
	case adb.Suspend:
		return a.tr("禁止")
	case adb.Unsuspend:
		return a.tr("恢复")
	case adb.Disable:
		return a.tr("停用")
	case adb.Enable:
		return a.tr("启用")
	}
	return string(action)
}

func (a *app) prepare(action adb.Action, targets ...adb.App) {
	if a.busy || a.confirmOpen {
		return
	}
	b := batch{serial: a.serial, user: a.userID, action: action, apps: slices.Clone(targets)}
	if len(targets) == 0 {
		for _, r := range a.rows {
			if r.selected {
				b.apps = append(b.apps, r.App)
			}
		}
	}
	if len(b.apps) > 0 {
		a.pending, a.confirmOpen = b, true
	}
}

func (a *app) executeBatch() {
	b, client, historyPath := a.pending, a.client, a.historyPath
	translate := a.translator()
	action := a.actionTitle(b.action)
	a.confirmOpen, a.results = false, nil
	a.run(fmt.Sprintf(a.tr("正在%s %d 个应用…"), action, len(b.apps)), func(ctx context.Context) (func(), error) {
		results, entries, saveErr := runBatch(ctx, &client, b, historyPath, translate)
		success := 0
		for i := range results {
			results[i].Action = action
			if results[i].OK {
				success++
			}
		}
		return func() {
			a.results, a.resultsOpen = results, true
			// Keep failed selections for retry and reconcile only confirmed successes.
			for _, r := range results {
				for i := range a.rows {
					if a.rows[i].PackageName == r.Package {
						for _, action := range r.Applied {
							switch action {
							case adb.Suspend, adb.Unsuspend:
								a.rows[i].Suspended = action == adb.Suspend
							case adb.Disable, adb.Enable:
								a.rows[i].Enabled = action == adb.Enable
							}
						}
						if r.OK {
							a.rows[i].selected = false
						}
					}
				}
				if r.OK && b.action == adb.Uninstall {
					a.rows = slices.DeleteFunc(a.rows, func(row appRow) bool { return row.PackageName == r.Package })
				}
			}
			if i := slices.IndexFunc(a.rows, func(row appRow) bool { return row.PackageName == a.detail.PackageName }); i >= 0 {
				a.detail = a.rows[i].App
			} else {
				a.detail, a.detailIcon, a.focusedRow = adb.App{}, nil, -1
			}
			a.status = fmt.Sprintf(a.tr("%s完成：%d 成功，%d 失败或取消 · %s / 用户 %d"), action, success, len(results)-success, b.serial, b.user)
			a.resultSummary = a.status
			a.failed = success != len(results)
			a.acceptHistory(entries, saveErr)
		}, nil
	})
}

func (a *app) modal(c *ui.Context, open *bool, title string, width, height float32, body func()) {
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35))
		panel.Width(width).MaxWidthPercent(92).Height(height).MaxHeightPercent(90).Padding(24).Gap(16).Radius(14).Background(c.Theme().Background).Border(1, c.Theme().Border).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(21).Bold().Grow(1)
				if ui.Button(c, a.tr("关闭")).Height(36).Padding(0, 16).Radius(18).Clicked() {
					*open = false
				}
			})
			body()
		})
	})
}

func (a *app) dialogs(c *ui.Context) {
	t := c.Theme()
	a.modal(c, &a.confirmOpen, a.tr("确认批量")+a.actionTitle(a.pending.action), 700, 560, func() {
		b := a.pending
		ui.Textf(c, a.tr("设备 %s · Android 用户 %d · %d 个应用"), b.serial, b.user, len(b.apps)).Bold()
		message := a.tr("禁止会先尝试暂停应用；设备不支持时自动改为停用。恢复会按应用状态解除暂停或重新启用，应用数据会保留。")
		if b.action == adb.Disable {
			message = a.tr("停用将对所选用户禁用应用，保留应用数据；可通过启用恢复。适用于不支持暂停的 Android 电视。")
		} else if b.action == adb.Enable {
			message = a.tr("启用将恢复所选用户已停用的应用，不会解除暂停状态。")
		} else if b.action == adb.Uninstall {
			message = a.tr("卸载将移除此用户的应用及其数据，无法撤销。系统应用仅对该用户移除，不会删除系统分区中的 APK。")
		}
		ui.Text(c, message).TextColor(t.Warning)
		if slices.ContainsFunc(b.apps, func(p adb.App) bool { return p.System }) {
			ui.Text(c, a.tr("包含系统应用：操作可能影响启动器、电话或其他系统功能。")).TextColor(t.Danger)
		}
		ui.List(c, nil, len(b.apps), func(i int) {
			ui.Column(c).MinHeight(56).Gap(4).Padding(8, 0).Children(func() {
				ui.Text(c, b.apps[i].Name).Bold()
				ui.Text(c, b.apps[i].PackageName).FontSize(12).TextColor(t.TextMuted)
			})
		}).Grow(1)
		ui.Row(c).Gap(12).Justify(ui.End).Children(func() {
			if ui.Button(c, a.tr("取消")).Height(40).Padding(0, 18).Radius(20).Clicked() {
				a.confirmOpen = false
			}
			if ui.PrimaryButton(c, fmt.Sprintf(a.tr("确认%s %d 个应用"), a.actionTitle(b.action), len(b.apps))).Height(40).Padding(0, 18).Radius(20).Disabled(a.busy).Clicked() {
				a.executeBatch()
			}
		})
	})
	a.modal(c, &a.resultsOpen, a.tr("操作结果"), 760, 550, func() {
		ui.Text(c, a.resultSummary).TextColor(t.TextMuted)
		if a.historyError != nil {
			ui.Text(c, a.historyErrorText()).FontSize(12).TextColor(t.Danger).Selectable()
		}
		ui.Text(c, a.tr("失败或取消后，请刷新应用确认设备状态。")).FontSize(12).TextColor(t.TextMuted)
		ui.List(c, nil, len(a.results), func(i int) {
			r := a.results[i]
			ui.Column(c).Gap(4).Padding(8, 0).Children(func() {
				ui.Text(c, r.Action+" · "+r.Package).Bold().Selectable()
				color := t.Danger
				if r.OK {
					color = t.Success
				}
				ui.Text(c, r.Message).TextColor(color).Selectable()
			})
		}).Grow(1)
	})
	a.detailDialog(c)
}

func (a *app) detailDialog(c *ui.Context) {
	t := c.Theme()
	a.modal(c, &a.detailOpen, a.tr("应用详情"), 800, 640, func() {
		p := a.detail
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			appIcon(c, a.detailIcon, 48)
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, p.Name).FontSize(20).Bold()
				ui.Text(c, p.PackageName+" · "+p.Version).Selectable().TextColor(t.TextMuted)
			})
			if ui.Button(c, a.tr("复制包名")).Height(40).Clicked() {
				c.Services().WriteClipboard(p.PackageName)
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, a.tr("签名证书")).Bold()
				ui.Text(c, a.tr("以下作者 / 公司为证书的自声明信息，不代表已认证的开发者身份。SHA-256 用于核对签名。")).FontSize(12).TextColor(t.TextMuted)
				if len(p.Signers) == 0 {
					ui.Text(c, a.tr("无法读取签名主体")).TextColor(t.TextMuted)
				}
				for _, s := range p.Signers {
					ui.Text(c, a.tr("公司：")+s.Organization+a.tr("\n主体：")+s.Subject+"\nSHA-256："+s.SHA256).Selectable()
				}
				for _, warning := range p.Warnings {
					ui.Text(c, warning).TextColor(t.Warning)
				}
				ui.Divider(c)
				ui.Textf(c, a.tr("申请的权限 · %d"), len(p.Permissions)).Bold()
				ui.Text(c, a.tr("授权状态来自 PackageManager；实际访问仍可能受 AppOps、一次性授权及设备策略限制。")).FontSize(12).TextColor(t.TextMuted)
				ui.SearchField(c, &a.permissionQuery).Height(40).Radius(20).Placeholder(a.tr("筛选权限…")).Label(a.tr("搜索权限"))
				for _, permission := range p.Permissions {
					if !strings.Contains(strings.ToLower(permission.Name), strings.ToLower(a.permissionQuery)) {
						continue
					}
					ui.Row(c).MinHeight(36).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, permission.Name).Grow(1).Selectable()
						state, color := a.tr("未授权"), t.TextMuted
						if permission.Granted {
							state, color = a.tr("已授权"), t.Success
						}
						ui.Text(c, state).TextColor(color)
					})
				}
			})
		})
	})
}

func main() {
	a := newApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = ctx
	a.loadPreferences()
	a.loadHistory()
	mygo.App.SetLanguages("en", "zh-Hans")
	mygo.App.OnQuit(cancel)
	mygo.App.OnLocaleChanged(func(locale string) {
		if a.settingsWindow != nil && !a.settingsWindow.IsDestroyed() {
			a.settingsWindow.SetTitle(translate(locale, "设置"))
		}
		a.invalidateWindows()
	})
	mygo.App.WhenReady(func() {
		if a.config.Language != "" {
			mygo.App.SetLocale(a.config.Language)
		}
		a.locale = mygo.App.Locale()
		a.openLauncher()
		a.refreshDevices()
		a.watchLauncher()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
