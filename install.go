package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"androidfun/internal/adb"
	"androidfun/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type apkClient interface {
	Install(context.Context, string, int, string) error
	Apps(context.Context, string, int) ([]adb.App, error)
}

func (a *app) canInstall() bool {
	return !a.busy && !a.confirmOpen && a.client.Path != "" && a.deviceOnline(a.serial) &&
		slices.ContainsFunc(a.users, func(user adb.User) bool { return user.ID == a.userID })
}

func (a *app) installFiles(paths []string) {
	if a.busy || a.confirmOpen {
		return
	}
	if !a.canInstall() {
		a.status, a.failed = a.tr("请先选择在线设备和 Android 用户。"), true
		return
	}
	if len(paths) != 1 {
		a.status, a.failed = a.tr("仅支持单个 APK 文件；请逐个安装。"), true
		return
	}
	a.startInstall(paths[0], false)
}

func (a *app) startInstall(path string, choose bool) {
	if !a.canInstall() {
		return
	}
	client, serial, user, window := a.client, a.serial, a.userID, a.win
	tr := a.translator()
	historyPath := a.historyPath
	previousStatus := a.status
	label := fmt.Sprintf(tr("正在安装 %s…"), filepath.Base(path))
	if choose {
		label = tr("选择 APK 文件")
	}
	a.run(label, func(ctx context.Context) (func(), error) {
		if choose {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent: window, Title: tr("选择 APK 文件"), ButtonLabel: tr("安装"),
				Message: fmt.Sprintf("%s · %s", serial, fmt.Sprintf(tr("用户 %d"), user)),
				Filters: []mygo.FileFilter{{Name: "Android APK", Extensions: []string{"apk"}}},
			})
			if err != nil {
				return nil, err
			}
			if len(paths) == 0 {
				return func() { a.status = previousStatus }, nil
			}
			path = paths[0]
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		window.Update(func() { a.status = fmt.Sprintf(tr("正在安装 %s…"), filepath.Base(absolute)) })
		return a.installWork(ctx, &client, serial, user, absolute, historyPath, tr)
	})
}

// The target and language are captured before the picker or worker starts.
func (a *app) installWork(ctx context.Context, client apkClient, serial string, user int, path, historyPath string, tr func(string) string) (func(), error) {
	err := client.Install(ctx, serial, user, path)
	installed := err == nil
	result := operationResult{Package: filepath.Base(path), Action: tr("安装"), Message: tr("成功"), OK: installed}
	if err != nil {
		result.Message = err.Error()
	}
	entry, saveErr := recordOperation(historyPath, serial, user, "install", result)
	var rows []appRow
	if installed {
		var apps []adb.App
		apps, err = client.Apps(ctx, serial, user)
		if err == nil {
			rows = makeRows(apps)
		} else {
			err = fmt.Errorf(tr("安装成功，但刷新应用失败：%v"), err)
		}
	}
	message := tr("成功")
	if err != nil {
		message = err.Error()
	}
	summary := fmt.Sprintf(tr("已安装 %s · %s / 用户 %d"), filepath.Base(path), serial, user)
	if !installed {
		summary = fmt.Sprintf("%s · %s / %s", filepath.Base(path), serial, fmt.Sprintf(tr("用户 %d"), user))
	}
	return func() {
		a.results = []operationResult{{Package: filepath.Base(path), Action: tr("安装"), Message: message, OK: installed}}
		a.resultSummary = summary
		if err != nil {
			a.resultSummary += " · " + message
		}
		if installed && err == nil {
			a.list = ui.ListState{Columns: a.list.Columns}
			a.setRows(rows)
			a.status = summary
		}
		a.acceptHistory([]history.Entry{entry}, saveErr)
	}, err
}
