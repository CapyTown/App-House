package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"androidfun/internal/adb"
	"github.com/egoist/mygo/ui"
)

func TestLanguageSelection(t *testing.T) {
	for _, test := range []struct {
		name, system, override, settings, userLabel string
	}{
		{"default", "", "", "Settings", "Work · User 10"},
		{"simplified Chinese", "zh-CN", "", "设置", "Work · 用户 10"},
		{"traditional Chinese", "zh-TW", "", "设置", "Work · 用户 10"},
		{"English", "en-US", "", "Settings", "Work · User 10"},
		{"unsupported language", "de-DE", "", "Settings", "Work · User 10"},
		{"English override", "zh-CN", "en", "Settings", "Work · User 10"},
		{"Chinese override", "en-US", "zh-Hans", "设置", "Work · 用户 10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := newApp()
			a.config.Language = test.override
			a.users, a.userID = []adb.User{{ID: 10, Name: "Work"}}, 10
			a.userLabel = a.userTitle(a.users[0])
			tester := ui.NewTester(func(c *ui.Context) {
				a.useLanguage(c)
				ui.Column(c).Children(func() {
					ui.Text(c, a.tr("设置"))
					ui.Text(c, a.userLabel)
				})
			}, 400, 120)
			tester.SetLocale(test.system)
			if !tester.HasText(test.settings) || !tester.HasText(test.userLabel) {
				t.Fatalf("language %q, override %q: %v", test.system, test.override, tester.Texts())
			}
		})
	}
}

func TestTranslatorCapturesOperationLanguage(t *testing.T) {
	a := newApp()
	a.locale = "zh-Hans"
	translateOperation := a.translator()
	a.locale = "en"
	if got := translateOperation("成功"); got != "成功" {
		t.Fatalf("operation language changed: %q", got)
	}
	if got := a.tr("成功"); got != "Succeeded" {
		t.Fatalf("current UI language did not change: %q", got)
	}
	if got := a.tr("com.example.app"); got != "com.example.app" {
		t.Fatalf("unknown app text changed: %q", got)
	}
}

func TestEnglishSettingsText(t *testing.T) {
	a := newApp()
	tester := ui.NewTester(a.settingsView, 640, 500)
	tester.SetLocale("en-US")
	for _, label := range []string{"Settings", "Language", "Follow system", "Device connections"} {
		if !tester.HasText(label) {
			t.Errorf("English settings missing %q", label)
		}
	}
	a.settingsTab = 1
	tester.Frame()
	if !tester.HasText("ADB executable") || !tester.HasText("Apply and find devices") {
		t.Fatal("ADB settings are not translated")
	}
	a.settingsTab = 2
	tester.Frame()
	if !tester.HasText("No history yet") {
		t.Fatal("empty history is not translated")
	}
}

func TestTranslationCatalog(t *testing.T) {
	if len(englishMessages) == 0 {
		t.Fatal("translation catalog is empty")
	}
	format := regexp.MustCompile(`%(?:\[[0-9]+\])?[+#\- .0-9*]*[a-zA-Z%]`)
	for key, value := range englishMessages {
		if value == "" || strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("missing English translation for %q: %q", key, value)
		}
		if !slices.Equal(format.FindAllString(key, -1), format.FindAllString(value, -1)) {
			t.Errorf("format placeholders changed: %q -> %q", key, value)
		}
		if strings.Count(key, "\n") != strings.Count(value, "\n") {
			t.Errorf("line breaks changed: %q -> %q", key, value)
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(source, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Errorf("%s: %v", fset.Position(literal.Pos()), err)
				return true
			}
			// Language names remain recognizable in their own language.
			if value == "简体中文" || !strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				return true
			}
			if _, ok := englishMessages[value]; !ok {
				t.Errorf("%s: untranslated source key %q", fset.Position(literal.Pos()), value)
			}
			return true
		})
	}
}
