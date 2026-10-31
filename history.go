package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"androidfun/internal/adb"
	"androidfun/internal/history"
	"androidfun/internal/preferences"
)

func (a *app) loadHistory() {
	path, err := preferences.Path()
	if err != nil {
		a.historyError = err
		return
	}
	a.historyPath = filepath.Join(filepath.Dir(path), "history.jsonl")
	a.history, a.historyError = history.Load(a.historyPath)
}

// Save on the worker before handing the result back to the window. Closing the
// window must not discard completed operations waiting for a UI update.
func recordOperation(path, serial string, user int, action string, result operationResult) (history.Entry, error) {
	entry := history.Entry{Time: time.Now(), Serial: serial, UserID: user, Action: action, Package: result.Package, Message: result.Message, OK: result.OK}
	if path == "" {
		return entry, errors.New("history file is unavailable")
	}
	return entry, history.Append(path, entry)
}

func (a *app) acceptHistory(entries []history.Entry, err error) {
	a.history = append(a.history, entries...)
	if err != nil {
		a.historyError = err
		a.status, a.failed = a.historyErrorText(), true
	}
}

func (a *app) historyErrorText() string {
	if a.historyError == nil {
		return ""
	}
	return a.tr("操作记录读写失败：") + a.historyError.Error()
}

func (a *app) historyAction(entry history.Entry) string {
	if entry.Action == "screenshot" {
		return a.tr("截屏")
	}
	if entry.Action == "record" {
		return a.tr("录屏")
	}
	if entry.Action == "install" {
		return a.tr("安装")
	}
	return a.actionTitle(adb.Action(entry.Action))
}

func (a *app) historyMessage(entry history.Entry) string {
	if entry.OK && (entry.Message == "成功" || entry.Message == "Success") {
		return a.tr("成功")
	}
	return a.tr(entry.Message)
}

func (a *app) historyMetadata(entry history.Entry) string {
	return fmt.Sprintf("%s · %s · %s", entry.Time.Local().Format("15:04:05"), entry.Serial, fmt.Sprintf(a.tr("用户 %d"), entry.UserID))
}

type historyRow struct {
	day   string
	index int // -1 is a date heading.
}

func groupHistory(entries []history.Entry) []historyRow {
	byDay := make(map[string][]int)
	var days []string
	for i := len(entries) - 1; i >= 0; i-- {
		day := entries[i].Time.Local().Format("2006-01-02")
		if _, exists := byDay[day]; !exists {
			days = append(days, day)
		}
		byDay[day] = append(byDay[day], i)
	}
	slices.Sort(days)
	slices.Reverse(days)
	rows := make([]historyRow, 0, len(entries)+len(days))
	for _, day := range days {
		rows = append(rows, historyRow{day: day, index: -1})
		for _, index := range byDay[day] {
			rows = append(rows, historyRow{day: day, index: index})
		}
	}
	return rows
}

type batchClient interface {
	Apply(context.Context, string, int, adb.Action, string) error
}

// Keep successful state changes even if a later step fails, and report the
// concrete command for history instead of calling a disabled app suspended.
func applyAppAction(ctx context.Context, client batchClient, b batch, app adb.App) (applied []adb.Action, actual adb.Action, err error) {
	apply := func(action adb.Action) error {
		actual = action
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := client.Apply(ctx, b.serial, b.user, action, app.PackageName); err != nil {
			return err
		}
		applied = append(applied, action)
		return nil
	}
	switch b.action {
	case adb.Suspend:
		err = apply(adb.Suspend)
		if unsupportedPackageCommand(err, adb.Suspend) {
			err = apply(adb.Disable)
		}
	case adb.Unsuspend:
		if !app.Enabled && !app.Suspended {
			err = apply(adb.Enable)
		} else {
			err = apply(adb.Unsuspend)
			if !app.Suspended && unsupportedPackageCommand(err, adb.Unsuspend) {
				err = apply(adb.Enable)
			} else if err == nil && !app.Enabled {
				err = apply(adb.Enable)
			}
		}
	default:
		err = apply(b.action)
	}
	return
}

func unsupportedPackageCommand(err error, action adb.Action) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	clean := strings.NewReplacer(":", " ", "'", " ", "\"", " ")
	for line := range strings.SplitSeq(strings.ToLower(err.Error()), "\n") {
		words := strings.Fields(clean.Replace(line))
		for i := 0; i+2 < len(words); i++ {
			if (words[i] == "unknown" || words[i] == "unsupported") && words[i+1] == "command" && words[i+2] == string(action) {
				return true
			}
		}
	}
	return false
}

func runBatch(ctx context.Context, client batchClient, b batch, path string, tr func(string) string) ([]operationResult, []history.Entry, error) {
	results := make([]operationResult, 0, len(b.apps))
	entries := make([]history.Entry, 0, len(b.apps))
	var saveErr error
	for _, p := range b.apps {
		result := operationResult{Package: p.PackageName}
		actual := b.action
		if ctx.Err() != nil {
			result.Message = tr("已取消，未执行")
		} else {
			var err error
			result.Applied, actual, err = applyAppAction(ctx, client, b, p)
			if err != nil {
				result.Message = err.Error()
				if len(result.Applied) > 0 {
					result.Message = fmt.Sprintf("%s: %s; %s", result.Applied[0], tr("成功"), result.Message)
				}
			} else {
				result.OK, result.Message = true, tr("成功")
				if len(result.Applied) > 1 {
					actual = b.action
					result.Message = tr("成功（已解除暂停并启用）")
				}
			}
		}
		entry, err := recordOperation(path, b.serial, b.user, string(actual), result)
		if saveErr == nil {
			saveErr = err
		}
		entries = append(entries, entry)
		results = append(results, result)
	}
	return results, entries, saveErr
}
