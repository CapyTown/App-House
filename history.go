package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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
	if entry.Action == "install" {
		return a.tr("安装")
	}
	return a.actionTitle(adb.Action(entry.Action))
}

func (a *app) historyMessage(entry history.Entry) string {
	if entry.OK {
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

func runBatch(ctx context.Context, client batchClient, b batch, path string, tr func(string) string) ([]operationResult, []history.Entry, error) {
	results := make([]operationResult, 0, len(b.apps))
	entries := make([]history.Entry, 0, len(b.apps))
	var saveErr error
	for _, p := range b.apps {
		result := operationResult{Package: p.PackageName}
		if ctx.Err() != nil {
			result.Message = tr("已取消，未执行")
		} else if err := client.Apply(ctx, b.serial, b.user, b.action, p.PackageName); err != nil {
			result.Message = err.Error()
		} else {
			result.OK, result.Message = true, tr("成功")
		}
		entry, err := recordOperation(path, b.serial, b.user, string(b.action), result)
		if saveErr == nil {
			saveErr = err
		}
		entries = append(entries, entry)
		results = append(results, result)
	}
	return results, entries, saveErr
}
