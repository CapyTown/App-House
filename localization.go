package main

import (
	_ "embed"
	"encoding/json/v2"
	"strings"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/ui"
)

//go:embed translations.json
var translationData []byte

var englishMessages = func() map[string]string {
	var messages map[string]string
	if err := json.Unmarshal(translationData, &messages); err != nil {
		panic(err)
	}
	return messages
}()

func translate(locale, message string) string {
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		return message
	}
	if text, ok := englishMessages[message]; ok {
		return text
	}
	return message
}

func (a *app) tr(message string) string { return translate(a.locale, message) }

// Workers capture a language instead of reading mutable window state.
func (a *app) translator() func(string) string {
	locale := a.locale
	return func(message string) string { return translate(locale, message) }
}

func (a *app) useLanguage(c *ui.Context) {
	locale := c.Locale()
	if a.config.Language != "" {
		locale = a.config.Language
	}
	if locale != a.locale {
		a.locale = locale
		for _, user := range a.users {
			if user.ID == a.userID {
				a.userLabel = a.userTitle(user)
				break
			}
		}
	}
}

func (a *app) languageSettings(c *ui.Context) {
	options := []string{a.tr("跟随系统"), "English", "简体中文"}
	codes := []string{"", "en", "zh-Hans"}
	current := 0
	for i, code := range codes {
		if a.config.Language == code {
			current = i
		}
	}
	selected := options[current]
	ui.Row(c).Padding(10, 24).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr("语言")).Grow(1)
		if ui.Select(c, &selected, options).Width(180).Label(a.tr("语言")).Disabled(a.busy || a.confirmOpen).Changed() {
			for i, option := range options {
				if option == selected {
					a.config.Language = codes[i]
					a.savePreferences()
					mygo.App.SetLocale(codes[i])
					a.invalidateWindows()
					break
				}
			}
		}
	})
}
